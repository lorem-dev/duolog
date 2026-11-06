package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/hook"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/lorem-dev/duolog/internal/workerenv"
	"github.com/spf13/cobra"
)

func isWorker() bool { return workerenv.IsWorker() }

// askWait turns policy.ask_timeout_seconds into the wait for an answer. Tests
// that only read the ask replace it, so they do not wait out a whole second.
var askWait = gate.AskWait

// readHookInput reads one hook's JSON, one byte over the limit so that callers can
// refuse it. Windows PowerShell 5.1 can start a pipe with a byte order mark, which
// JSON rejects: a hook then registered no lead and said nothing.
func readHookInput(cmd *cobra.Command) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4*1024*1024+1))
	return bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), err
}

func newGateCommand() *cobra.Command {
	var agent, event string
	cmd := &cobra.Command{Use: "gate", Short: "Decide a worker hook request", SilenceErrors: true, RunE: func(cmd *cobra.Command, args []string) error {
		started := time.Now()
		// A lead records its live mode and keeps person-only approvals for the person.
		if !isWorker() {
			defer func() { _ = recover() }()
			root, err := projectRoot()
			if err == nil {
				data, readErr := readHookInput(cmd)
				if readErr == nil && len(data) <= 4*1024*1024 {
					if event == "PreToolUse" {
						if d, ok := leadGuard(filepath.Join(root, ".duolog"), agent, data); ok {
							_, _ = cmd.OutOrStdout().Write(gate.RenderLead(agent, d))
						}
					}
					hook.Observe(cmd.Context(), hook.Env{StartedAt: started, Root: root, WorkRoot: workRoot(root), Agent: agent, Input: data, Environ: os.Environ()})
				}
			}
			return nil
		}
		a := gate.Action{Kind: "unknown", Role: os.Getenv("DUOLOG_ROLE")}
		d := gate.Decision{Kind: gate.Deny, Mode: mode.Ask, Rule: "gate setup"}
		root, err := projectRoot()
		var s *store.Store
		if err == nil {
			// A worker runs in a project with state. Elsewhere it denies and creates none.
			if info, statErr := os.Stat(filepath.Join(root, ".duolog")); statErr != nil || !info.IsDir() {
				err = fmt.Errorf("no .duolog folder in %s", root)
			} else {
				s, err = store.Open(root)
			}
		}
		if err == nil {
			a, d, err = evaluateHook(cmd, root, agent, event, started)
		}
		if err != nil {
			d.Kind, d.Reason, d.Rule = gate.Deny, "gate failed closed: "+err.Error(), "gate setup"
		}
		d.Native = os.Getenv("DUOLOG_NATIVE")
		if s != nil {
			if logErr := gate.Log(s, a, d); logErr != nil {
				d.Kind, d.Reason, d.Rule = gate.Deny, "cannot log gate decision: "+logErr.Error(), "decision log"
			}
		}
		if event != "PreToolUse" && event != "PermissionRequest" {
			event = "PreToolUse"
		}
		_, err = cmd.OutOrStdout().Write(gate.Render(agent, event, d))
		return err
	}}
	cmd.Flags().StringVar(&agent, "agent", "", "Agent receiving this hook")
	cmd.Flags().StringVar(&event, "event", "", "PreToolUse or PermissionRequest")
	return cmd
}

func evaluateHook(cmd *cobra.Command, root, agent, event string, started time.Time) (gate.Action, gate.Decision, error) {
	a := gate.Action{Kind: "unknown", Role: os.Getenv("DUOLOG_ROLE")}
	d := gate.Decision{Mode: mode.Ask}
	if event != "PreToolUse" && event != "PermissionRequest" {
		return a, d, fmt.Errorf("unsupported gate event %q", event)
	}
	data, err := readHookInput(cmd)
	if err != nil {
		return a, d, err
	}
	if len(data) > 4*1024*1024 {
		return a, d, fmt.Errorf("hook input exceeds 4 MiB")
	}
	actions, err := gate.NormalizeAll(agent, data)
	if err != nil {
		return a, d, err
	}
	var envelope hookEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return a, d, err
	}
	// Every action carries the store roots, even when collection is off or the
	// roots do not exist yet. Roots that cannot be placed fail closed.
	roots, err := gateProtectedRoots()
	if err != nil {
		return a, d, fmt.Errorf("cannot place the private usage stores: %w", err)
	}
	work := workRoot(root)
	for i := range actions {
		actions[i].Role = a.Role
		action := &actions[i]
		action.ProtectedRoots = roots
		action.Peer = os.Getenv("DUOLOG_PEER")
		action.Work = work
		ownClaudeFiles(action, i == 0, agent, work, envelope)
		if (action.Kind == "read" || action.Kind == "write") && action.Target != "" {
			action.Target = gate.RelativeTarget(work, envelope.Cwd, action.Target)
		}
		if action.Kind == "exec" || action.Search {
			action.Work, action.Cwd = work, envelope.Cwd
			action.State = root
		}
	}
	a = actions[0]
	cfg, err := config.Load(root)
	if err != nil {
		return a, d, err
	}
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		return a, d, err
	}
	lead, m, err := gate.LeadMode(reg, session.CurrentLead(os.Environ()), started)
	if err != nil {
		return a, d, err
	}
	// A delegate names its worker's level; the gate takes it only to lower.
	m = gate.WorkerMode(m, os.Getenv("DUOLOG_MODE"))
	d.Mode = m
	rules, err := gate.ImportLead(work, lead.Agent)
	if err != nil {
		return a, d, err
	}
	if a.Role == "" {
		var hook struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(data, &hook); err != nil {
			return a, d, err
		}
		entries, err := reg.Entries()
		if err != nil {
			return a, d, err
		}
		for _, entry := range entries {
			if !entry.Closed && entry.Lead == lead.ID && hook.SessionID != "" && (entry.ThreadID == hook.SessionID || entry.ID == hook.SessionID) {
				a.Role = entry.Role
				break
			}
		}
	}
	if role, ok := cfg.Roles[a.Role]; ok {
		rules.RoleSandbox = role.Sandbox
	} else {
		rules.RoleSandbox = "read-only"
	}
	// Bare native-tool denies remain enforceable even for normalized tools.
	for _, rule := range rules.Deny {
		if rule.Action == "unknown" && rule.Match == envelope.Tool {
			return a, gate.Decision{Kind: gate.Deny, Mode: m, Reason: "denied by lead settings", Rule: "lead tool: " + envelope.Tool}, nil
		}
	}
	d = gate.Decision{Kind: gate.Allow, Mode: m}
	var asked []string
	person := false
	// initial is the rule that asked, kept for telemetry: the person's rule
	// when one action needs the person, since the person then answers.
	var initial gate.Decision
	for _, action := range actions {
		action.Role = a.Role
		next := gate.Evaluate(cfg.Policy, rules, m, action)
		person = person || gate.PersonOnly(next)
		if next.Kind == gate.Deny {
			return action, next, nil
		}
		if next.Kind == gate.Ask {
			asked = append(asked, action.Target)
			if initial.Kind != gate.Ask || gate.PersonOnly(next) && !gate.PersonOnly(initial) {
				initial = next
			}
		}
		if d.Kind != gate.Ask {
			a, d = action, next
		}
	}
	if d.Kind == gate.Ask {
		// One answer covers the whole tool call, so the question names every path.
		a.Target = strings.Join(asked, " ")
		b, err := bus.Open(reg.Dir, cfg.Wire)
		if err != nil {
			return a, d, err
		}
		if err := gate.LogRequest(&store.Store{Dir: reg.Dir}, a, d); err != nil {
			return a, d, err
		}
		wait := askWait(cfg.Policy.AskTimeoutSeconds)
		// The store opens only once an ask has ended: allows and denies never
		// reach telemetry.
		ctx := gate.WithAskObserver(cmd.Context(), func(r gate.AskResult) {
			if tel := openTelemetry(cfg, root); tel != nil {
				defer tel.Close()
				recordGate(tel, a, initial, r)
			}
		})
		resolved := gate.Escalate(ctx, b, lead.ID, a, wait, person)
		resolved.Mode = d.Mode
		d = resolved
	}
	return a, d, nil
}

// ownToolResult says whether a Claude read targets the saved tool results of
// the hook's own session. The session id comes from the hook input.
func ownToolResult(sessionID, cwd, target string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if rest, ok := strings.CutPrefix(target, "~/"); ok {
		target = filepath.Join(home, rest)
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(cwd, target)
	}
	return gate.OwnToolResults(trusthome.Claude(home), sessionID, target)
}

// claudeTempBase is where Claude keeps its claude-<uid> temp folders on a
// POSIX host; tests set another.
var claudeTempBase = "/tmp"

// hookEnvelope is what the gate reads of a hook's input besides the actions.
type hookEnvelope struct {
	Cwd       string `json:"cwd"`
	Tool      string `json:"tool_name"`
	SessionID string `json:"session_id"`
	// The read tools' path as the worker wrote it, before Normalize cleans
	// away its .. parts.
	Input struct {
		File string `json:"file_path"`
		Path string `json:"path"`
	} `json:"tool_input"`
}

// ownClaudeFiles marks what of a Claude worker's own an action reaches, from
// the hook's session id: a read of its saved tool results, or of its session
// temp folder by the tool's path, which is the first action (a Glob or Grep
// pattern follows it); and, for an exec action, that temp folder, whose files
// its named read commands may read.
func ownClaudeFiles(action *gate.Action, first bool, agent, work string, e hookEnvelope) {
	if agent != "claude" {
		return
	}
	temps := claudeTemps(e.SessionID, work, e.Cwd)
	if action.Kind == "read" && action.Target != "" {
		action.OwnResult = ownToolResult(e.SessionID, e.Cwd, action.Target) || first && ownTempPath(temps, e.Cwd, e.Input.File, e.Input.Path)
	}
	if action.Kind == "exec" {
		action.Temp = temps
	}
}

// claudeTemps names the temp folder of a Claude worker's own session, from
// the session id in the hook input, never from the worker. Claude names the
// folder after the one the session started in: the work root, or the hook's
// folder. Both candidates hold that one session id.
func claudeTemps(sessionID string, folders ...string) []string {
	var temps []string
	for _, folder := range folders {
		if temp := gate.ClaudeTemp(claudeTempBase, os.Getuid(), sessionID, folder); temp != "" && !slices.Contains(temps, temp) {
			temps = append(temps, temp)
		}
	}
	return temps
}

// ownTempPath reports whether a read tool's path, as the worker wrote it,
// lies in the worker's own session temp folder. Read names it file_path; Grep,
// Glob and LS name it path.
func ownTempPath(temps []string, cwd, file, path string) bool {
	target := file
	if target == "" {
		target = path
	}
	if target == "" || len(temps) == 0 {
		return false
	}
	if !filepath.IsAbs(target) {
		if cwd == "" {
			return false
		}
		// A plain join keeps every .. for InClaudeTemp to refuse.
		target = cwd + "/" + target
	}
	return gate.InClaudeTemp(temps, target)
}

// gateProtectedRoots names the telemetry and balance roots for the worker
// gate. It only computes the paths: the worker hook never opens the store or
// creates its salt.
func gateProtectedRoots() ([]string, error) {
	home, _ := os.UserHomeDir()
	l := telemetry.Location{GOOS: runtime.GOOS, Home: home, XDGStateHome: os.Getenv("XDG_STATE_HOME"), LocalAppData: os.Getenv("LOCALAPPDATA")}
	telemetryRoot, err := telemetry.ResolveRoot(l)
	if err != nil {
		return nil, err
	}
	balanceRoot, err := telemetry.BalanceRoot(l)
	if err != nil {
		return nil, err
	}
	return []string{telemetryRoot, balanceRoot}, nil
}

func newDecisionsCommand() *cobra.Command {
	return &cobra.Command{Use: "decisions", Short: "Show recorded permission decisions and reasons", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		for _, name := range []string{"requests.ndjson", "decisions.ndjson"} {
			f, err := os.Open(filepath.Join(root, ".duolog", name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			defer f.Close() //nolint:gocritic // two files, both held until the report is written
			decoder := json.NewDecoder(f)
			for {
				var row gate.Record
				if err := decoder.Decode(&row); err == io.EOF {
					break
				} else if err != nil {
					return err
				}
				label := map[string]string{gate.Allow: "Allow", gate.Deny: "Deny", gate.Ask: "Ask"}[row.Kind]
				modeText := string(row.Mode)
				if row.Native != "" {
					modeText += "; native " + row.Native
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s (mode %s; rule %s) Reason: %s\n", label, row.Action.Kind, row.Action.Target, modeText, row.Rule, row.Reason); err != nil {
					return err
				}
			}
		}
		return nil
	}}
}

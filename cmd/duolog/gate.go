package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/hook"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/lorem-dev/duolog/internal/workerenv"
	"github.com/spf13/cobra"
)

func isWorker() bool { return workerenv.IsWorker() }

// readHookInput reads one hook's JSON, one byte over the limit so that callers can
// refuse it. Windows PowerShell 5.1 can start a pipe with a byte order mark, which
// JSON rejects: a hook then registered no lead and said nothing.
func readHookInput(cmd *cobra.Command) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4*1024*1024+1))
	return bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), err
}

func newGateCommand(deps *cmdDeps) *cobra.Command {
	var agent, event string
	cmd := &cobra.Command{Use: "gate", Short: "Decide a worker hook request", SilenceErrors: true, RunE: func(cmd *cobra.Command, args []string) error {
		started := time.Now()
		// A lead records its live mode and keeps person-only approvals for the
		// person, unless it runs in bypass or auto (guardLeadCall).
		if !isWorker() {
			defer func() { _ = recover() }()
			root, err := projectRoot()
			if err == nil {
				data, readErr := readHookInput(cmd)
				if readErr == nil && len(data) <= 4*1024*1024 {
					if event == "PreToolUse" {
						if d, ok := guardLeadCall(project.LeadHome(root), agent, data); ok {
							_, _ = cmd.OutOrStdout().Write(policy.RenderLead(agent, d))
						}
					}
					hook.Observe(cmd.Context(), wiring.HookEnv(hook.Env{StartedAt: started, Root: root, WorkRoot: workRoot(root), Agent: agent, Input: data, Environ: os.Environ()}))
				}
			}
			return nil
		}
		a := policy.Action{Kind: "unknown", Role: os.Getenv("DUOLOG_ROLE")}
		d := policy.Decision{Kind: policy.Deny, Mode: mode.Ask, Rule: "gate setup"}
		root, err := projectRoot()
		var reg *session.Registry
		if err == nil {
			// A worker runs in a project with state. Elsewhere it denies and creates none.
			if info, statErr := os.Stat(filepath.Join(root, ".duolog")); statErr != nil || !info.IsDir() {
				err = fmt.Errorf("no .duolog folder in %s", root)
			} else {
				reg, err = session.Open(project.LeadHome(root))
			}
		}
		if err == nil {
			a, d, err = evaluateHook(deps, cmd, reg, root, agent, event, started)
		}
		if err != nil {
			d.Kind, d.Reason, d.Rule = policy.Deny, "gate failed closed: "+err.Error(), "gate setup"
		}
		d.Native = os.Getenv("DUOLOG_NATIVE")
		if reg != nil {
			loggedAction, loggedDecision := safeGateFields(a, d)
			if logErr := gate.Log(&store.Store{Dir: reg.Dir}, loggedAction, loggedDecision); logErr != nil {
				d.Kind, d.Reason, d.Rule = policy.Deny, "cannot log gate decision: "+logErr.Error(), "decision log"
			}
		}
		if event != "PreToolUse" && event != "PermissionRequest" {
			event = "PreToolUse"
		}
		_, err = cmd.OutOrStdout().Write(policy.Render(agent, event, d))
		return err
	}}
	cmd.Flags().StringVar(&agent, "agent", "", "Agent receiving this hook")
	cmd.Flags().StringVar(&event, "event", "", "PreToolUse or PermissionRequest")
	return cmd
}

func evaluateHook(deps *cmdDeps, cmd *cobra.Command, reg *session.Registry, root, agent, event string, started time.Time) (policy.Action, policy.Decision, error) {
	a := policy.Action{Kind: "unknown", Role: os.Getenv("DUOLOG_ROLE")}
	d := policy.Decision{Mode: mode.Ask}
	if event != "PreToolUse" && event != "PermissionRequest" {
		return a, d, fmt.Errorf("unsupported gate event %q", event)
	}
	actions, envelope, err := readGateActions(cmd, root, agent)
	if err != nil {
		return a, d, err
	}
	a = actions[0]
	cfg, err := configfile.Load(root)
	if err != nil {
		return a, d, err
	}
	lead, m, err := gate.LeadMode(reg, session.CurrentLead(os.Environ()), started)
	if err != nil {
		return a, d, err
	}
	// A delegate names its worker's level; the gate takes it only to lower.
	m = policy.WorkerMode(m, os.Getenv("DUOLOG_MODE"))
	d.Mode = m
	rules, err := hookRules(cfg, reg, lead, &a, root, envelope.SessionID)
	if err != nil {
		return a, d, err
	}
	result := evaluateGateActions(cfg, rules, m, actions, a.Role, envelope.Tool)
	a, d = result.action, result.decision
	if d.Kind == policy.Ask {
		// One answer covers the whole tool call, so the question names every path.
		a.Target = strings.Join(result.asked, " ")
		key := gate.AskKey(envelope.SessionID, envelope.ToolUseID, askQuestion(a.Kind, result.asked, envelope.Cwd), result.person)
		ctx := gate.WithAskFiled(cmd.Context(), personAskNotice(cfg, deps.Notify))
		resolved, err := askLead(ctx, deps.AskWait(askSeconds(cfg, result.person), result.person, deps.HookLimit(agent, event, root, a.Work, os.Environ())), cfg, root, reg.Dir, lead.ID, key, a, d, result.initial, result.person)
		if err != nil {
			return a, d, err
		}
		d = resolved
	}
	return a, d, nil
}

func readGateActions(cmd *cobra.Command, root, agent string) ([]policy.Action, hookEnvelope, error) {
	var envelope hookEnvelope
	data, err := readHookInput(cmd)
	if err != nil {
		return nil, envelope, err
	}
	if len(data) > 4*1024*1024 {
		return nil, envelope, fmt.Errorf("hook input exceeds 4 MiB")
	}
	actions, err := policy.NormalizeAll(agent, data)
	if err != nil {
		return nil, envelope, err
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, envelope, err
	}
	// Every action carries the store roots, even when collection is off or the
	// roots do not exist yet. Roots that cannot be placed fail closed.
	roots, err := gateProtectedRoots()
	if err != nil {
		return nil, envelope, fmt.Errorf("cannot place the private usage stores: %w", err)
	}
	work := workRoot(root)
	for i := range actions {
		actions[i].Role = os.Getenv("DUOLOG_ROLE")
		action := &actions[i]
		action.ProtectedRoots = roots
		action.Peer = os.Getenv("DUOLOG_PEER")
		action.Work = work
		workerFileAction(action, i, agent, work, envelope)
		if (action.Kind == "read" || action.Kind == "write") && action.Target != "" {
			action.Target = policy.RelativeTarget(work, envelope.Cwd, action.Target)
		}
		if action.Kind == "exec" || action.Search {
			action.Work, action.Cwd = work, envelope.Cwd
			action.State = root
		}
	}
	return actions, envelope, nil
}

func hookRules(cfg *config.Config, reg *session.Registry, lead session.Lead, a *policy.Action, root, sessionID string) (policy.LeadRules, error) {
	// The ceiling holds the settings of the lead's own checkout and the work's.
	// root, the work's main checkout, places a lead of the same project
	// without Git.
	rules, err := gate.ImportLeadForWorkState(a.Work, root, gate.LeadRoot(lead, reg), lead.Agent)
	if err != nil {
		return rules, err
	}
	if a.Role == "" {
		entries, err := reg.Entries()
		if err != nil {
			return rules, err
		}
		for _, entry := range entries {
			if !entry.Closed && entry.Lead == lead.ID && sessionID != "" && (entry.ThreadID == sessionID || entry.ID == sessionID) {
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
	return rules, nil
}

type gateEvaluation struct {
	action            policy.Action
	decision, initial policy.Decision
	asked             []string
	person            bool
}

func evaluateGateActions(cfg *config.Config, rules policy.LeadRules, m mode.Mode, actions []policy.Action, role, tool string) gateEvaluation {
	r := gateEvaluation{action: actions[0], decision: policy.Decision{Kind: policy.Allow, Mode: m}}
	r.action.Role = role
	// Bare native-tool denies remain enforceable even for normalized tools.
	for _, rule := range rules.Deny {
		if rule.Action == "unknown" && rule.Match == tool {
			r.decision = policy.Decision{Kind: policy.Deny, Mode: m, Reason: "denied by lead settings", Rule: "lead tool: " + tool}
			return r
		}
	}
	// initial is the rule that asked, kept for telemetry: the person's rule
	// when one action needs the person, since the person then answers.
	for _, action := range actions {
		action.Role = role
		next := policy.Evaluate(cfg.Policy, rules, m, action)
		r.person = r.person || policy.PersonOnly(next)
		if next.Kind == policy.Deny {
			r.action, r.decision = action, next
			return r
		}
		if next.Kind == policy.Ask {
			r.asked = append(r.asked, action.Target)
			if r.initial.Kind != policy.Ask || policy.PersonOnly(next) && !policy.PersonOnly(r.initial) {
				r.initial = next
			}
		}
		if r.decision.Kind != policy.Ask {
			r.action, r.decision = action, next
		}
	}
	return r
}

// askSeconds is the configured wait of an ask that the lead or only the person
// can answer.
func askSeconds(cfg *config.Config, person bool) int {
	if person {
		return cfg.Policy.PersonAskTimeoutSeconds
	}
	return cfg.Policy.AskTimeoutSeconds
}

// askQuestion is the text that keys an ask record: the kind, each asked path
// after its length, and the folder. Joined with spaces alone, a file named
// "a b" and the files a and b would read the same.
func askQuestion(kind string, asked []string, cwd string) string {
	var b strings.Builder
	b.WriteString(kind)
	for _, path := range asked {
		b.WriteString(" " + strconv.Itoa(len(path)) + ":" + path)
	}
	return b.String() + " " + cwd
}

// personAskNotice is the filed callback of a gate's ask: a desktop notice for
// an ask that only the person can answer, as the lead cannot. A lead ask, a
// nil notify and lead.notify false give none.
func personAskNotice(cfg *config.Config, notify func(title, message string)) func(int64, bool, string) {
	if notify == nil || cfg == nil || !cfg.Lead.Notifies() {
		return nil
	}
	return func(n int64, person bool, summary string) {
		if person {
			notify("duolog", fmt.Sprintf("ask %d waits for you: %s", n, summary))
		}
	}
}

// askLead sends the ask of one tool call to the lead. PreToolUse and
// PermissionRequest both reach it for one Claude tool call: the first asks and
// the other takes its answer. Only a reply is shared.
func askLead(ctx context.Context, wait time.Duration, cfg *config.Config, root, stateDir, leadID, key string, a policy.Action, d, initial policy.Decision, person bool) (policy.Decision, error) {
	b, err := bus.Open(stateDir, cfg.Wire)
	if err != nil {
		return d, err
	}
	loggedAction, loggedDecision := safeGateFields(a, d)
	if err := gate.LogRequest(&store.Store{Dir: stateDir}, loggedAction, loggedDecision); err != nil {
		return d, err
	}
	resolved := gate.EscalateOnce(ctx, stateDir, key, wait, func(left time.Duration) (policy.Decision, bool) {
		var replied bool
		// The store opens only once an ask has ended: allows and denies never
		// reach telemetry.
		observed := gate.WithAskObserver(ctx, func(r gate.AskResult) {
			replied = r.By != "auto" && (r.Answer == policy.Allow || r.Answer == policy.Deny)
			if tel := openTelemetry(cfg, root); tel != nil {
				defer tel.Close()
				recordGate(tel, a, initial, r)
			}
		})
		return gate.Escalate(observed, b, leadID, loggedAction, left, person), replied
	})
	resolved.Mode = d.Mode
	return resolved, nil
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
	ToolUseID string `json:"tool_use_id"`
	// The read tools' path and patterns as the worker wrote them, before
	// Normalize cleans away their .. parts.
	Input struct {
		File     string `json:"file_path"`
		Notebook string `json:"notebook_path"`
		Patch    string `json:"patch"`
		Command  string `json:"command"`
		Path     string `json:"path"`
		Pattern  string `json:"pattern"`
		Glob     string `json:"glob"`
	} `json:"tool_input"`
}

// fileTargets preserves filesystem traversal order before NormalizeAll cleans paths.
func (e hookEnvelope) fileTargets() []string {
	if e.Tool == "apply_patch" {
		return policy.PatchPaths(e.Input.Patch + "\n" + e.Input.Command)
	}
	target := e.Input.File
	if target == "" {
		target = e.Input.Path
	}
	if target == "" {
		target = e.Input.Notebook
	}
	out := []string{target}
	if e.Tool == "Glob" {
		out = append(out, e.Input.Pattern)
	}
	if e.Tool == "Grep" {
		out = append(out, e.Input.Glob)
	}
	return out
}

func workerFileAction(action *policy.Action, index int, agent, work string, envelope hookEnvelope) {
	if action.Kind == "read" || action.Kind == "write" {
		if raw := envelope.fileTargets(); index < len(raw) && raw[index] != "" {
			action.Target = raw[index]
		}
	}
	workerFiles(action, index == 0, agent, work, envelope)
}

// workerFiles marks what of the worker's own an action may read: the paths
// its task lists under Read, from DUOLOG_READ, which delegate sets and the
// worker cannot, and a Claude worker's own files (ownClaudeFiles). A read
// tool that wrote .. in its path or pattern gets no Read list, since
// Normalize cleaned it away before a symlink could be resolved.
func workerFiles(action *policy.Action, first bool, agent, work string, e hookEnvelope) {
	raw := []string{e.Input.File, e.Input.Path, e.Input.Pattern, e.Input.Glob}
	if !slices.ContainsFunc(raw, dotDot) {
		action.Reads = gateReads(os.Getenv(policy.TaskReadsEnv))
		action.WideRead = wideSkillRoot
	}
	ownClaudeFiles(action, first, agent, work, e)
}

// wideSkillRoot reports a resolved agent skill root that is too wide to read,
// by the check the Read list of a task gets.
func wideSkillRoot(path string) bool { return configfile.RefuseRead(path) != "" }

// gateReads are the entries of a DUOLOG_READ value that are not too wide to
// read (configfile.RefuseRead), judged again here after the symlinks of each
// entry's fixed folder are resolved, since a link can point elsewhere now
// than when delegate started the worker.
func gateReads(value string) []string {
	return slices.DeleteFunc(policy.TaskReads(value), func(entry string) bool {
		return configfile.RefuseRead(pathutil.Resolved(policy.ReadEntryFolder(pathutil.Native(entry)))) != ""
	})
}

// dotDot reports whether a path names a .. segment, with either separator.
func dotDot(path string) bool {
	return slices.Contains(strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }), "..")
}

// ownClaudeFiles marks what of a Claude worker's own an action reaches, from
// the hook's session id: a read of its saved tool results, or of its session
// temp folder by the tool's path, which is the first action (a Glob or Grep
// pattern follows it); and, for an exec action, that temp folder, whose files
// its named read commands may read.
func ownClaudeFiles(action *policy.Action, first bool, agent, work string, e hookEnvelope) {
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
	return policy.InClaudeTemp(temps, target)
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
	return &cobra.Command{Use: "decisions", Short: "Show recorded permission decisions and reasons", Args: cobra.NoArgs, PreRunE: refuseWorkerCommand, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		home, work := project.LeadHome(root), workRoot(root)
		for _, name := range []string{"requests.ndjson", "decisions.ndjson"} {
			f, err := os.Open(filepath.Join(home, name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			defer f.Close() //nolint:gocritic // two files, both held until the report is written
			if err := printDecisions(cmd.OutOrStdout(), f, work); err != nil {
				return err
			}
		}
		return nil
	}}
}

func printDecisions(out io.Writer, in io.Reader, work string) error {
	decoder := json.NewDecoder(in)
	for {
		var row gate.Record
		if err := decoder.Decode(&row); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, decisionText(row, work)); err != nil {
			return err
		}
	}
}

func decisionText(row gate.Record, work string) string {
	label := map[string]string{policy.Allow: "Allow", policy.Deny: "Deny", policy.Ask: "Ask"}[row.Kind]
	modeText := string(row.Mode)
	if row.Native != "" {
		modeText += "; native " + row.Native
	}
	where := ""
	if row.Action.Work != "" && checkoutKey(row.Action.Work) != checkoutKey(work) {
		where = " (work " + row.Action.Work + ")"
	}
	return fmt.Sprintf("%s %s %s%s (mode %s; rule %s) Reason: %s", label, row.Action.Kind, row.Action.Target, where, modeText, row.Rule, row.Reason)
}

// Recognized credential forms are removed from persistent records and ask
// summaries; policy evaluation always sees the original action.
var gateCredentials = []*regexp.Regexp{
	regexp.MustCompile(`(?i)((?:^|\s)(?:-u|--user|--proxy-user)(?:\s+|=))(?:"[^"]*"|'[^']*'|[^\s;&]+)`),
	// curl -uuser:secret: an attached value counts only with a colon, so
	// sort -u or rg -u stay as they are.
	regexp.MustCompile(`((?:^|\s)-u)[^\s;&'":]*:[^\s;&]+`),
	regexp.MustCompile(`(?i)(authorization["']?\s*:\s*)(?:bearer\s+|basic\s+)?[^'"\r\n]+`),
	regexp.MustCompile(`(?i)((?:[a-z0-9_]*(?:token|password|passwd|secret|api[_-]?key)|--(?:token|password|secret|api-key))\s*[=: ]\s*)(?:"[^"]*"|'[^']*'|[^\s;&]+)`),
	regexp.MustCompile(`(?i)(https?://)[^/@\s]+:[^/@\s]+@`),
}

func safeGateFields(a policy.Action, d policy.Decision) (policy.Action, policy.Decision) {
	redact := func(value string) string {
		for _, re := range gateCredentials {
			value = re.ReplaceAllString(value, "${1}[redacted]")
		}
		return value
	}
	a.Target, d.Reason, d.Rule = redact(a.Target), redact(d.Reason), redact(d.Rule)
	return a, d
}

func refuseWorkerCommand(cmd *cobra.Command, _ []string) error {
	if isWorker() {
		return fmt.Errorf("workers cannot run %s", cmd.Name())
	}
	return nil
}

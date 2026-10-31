package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/hook"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/workerenv"
	"github.com/spf13/cobra"
)

func isWorker() bool { return workerenv.IsWorker() }

func newGateCommand() *cobra.Command {
	var agent, event string
	cmd := &cobra.Command{Use: "gate", Short: "Decide a worker hook request", SilenceErrors: true, RunE: func(cmd *cobra.Command, args []string) error {
		started := time.Now()
		// A lead only records its live mode; it never receives a gate decision.
		if !isWorker() {
			defer func() { _ = recover() }()
			root, err := projectRoot()
			if err == nil {
				data, readErr := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4*1024*1024+1))
				if readErr == nil && len(data) <= 4*1024*1024 {
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
	data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4*1024*1024+1))
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
	var envelope struct {
		Cwd  string `json:"cwd"`
		Tool string `json:"tool_name"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return a, d, err
	}
	work := workRoot(root)
	for i := range actions {
		actions[i].Role = a.Role
		action := &actions[i]
		if (action.Kind == "read" || action.Kind == "write") && action.Target != "" {
			action.Target = gate.RelativeTarget(work, envelope.Cwd, action.Target)
		}
		if action.Kind == "exec" {
			action.Work, action.Cwd = work, envelope.Cwd
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
	for _, action := range actions {
		action.Role = a.Role
		next := gate.Evaluate(cfg.Policy, rules, m, action)
		if next.Kind == gate.Deny {
			return action, next, nil
		}
		if next.Kind == gate.Ask {
			asked = append(asked, action.Target)
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
		wait := gate.AskWait(cfg.Policy.AskTimeoutSeconds)
		resolved := gate.Escalate(cmd.Context(), b, lead.ID, a, wait)
		resolved.Mode = d.Mode
		d = resolved
	}
	return a, d, nil
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
			defer f.Close()
			decoder := json.NewDecoder(f)
			for {
				var row gate.Record
				if err := decoder.Decode(&row); err == io.EOF {
					break
				} else if err != nil {
					return err
				}
				label := map[string]string{gate.Allow: "Allow", gate.Deny: "Deny", gate.Ask: "Ask"}[row.Kind]
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s (mode %s; rule %s) Reason: %s\n", label, row.Action.Kind, row.Action.Target, row.Mode, row.Rule, row.Reason); err != nil {
					return err
				}
			}
		}
		return nil
	}}
}

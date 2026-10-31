package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/hook"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

func isWorker() bool {
	depth, _ := strconv.Atoi(os.Getenv("DUOLOG_DEPTH"))
	return depth >= 1
}

func newGateCommand() *cobra.Command {
	var agent, event string
	cmd := &cobra.Command{Use: "gate", Short: "Decide a worker hook request", SilenceErrors: true, RunE: func(cmd *cobra.Command, args []string) error {
		// A lead only records its live mode; it never receives a gate decision.
		if !isWorker() {
			root, err := projectRoot()
			if err == nil {
				data, readErr := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4*1024*1024+1))
				if readErr == nil && len(data) <= 4*1024*1024 {
					hook.Observe(cmd.Context(), hook.Env{Root: root, Agent: agent, Input: data, Environ: os.Environ()})
				}
			}
			return nil
		}
		a := gate.Action{Kind: "unknown", Role: os.Getenv("DUOLOG_ROLE")}
		d := gate.Decision{Kind: gate.Deny, Mode: mode.Ask, Rule: "gate setup"}
		root, err := projectRoot()
		var s *store.Store
		if err == nil {
			s, err = store.Open(root)
		}
		if err == nil {
			a, d, err = evaluateHook(cmd, root, agent, event)
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

func evaluateHook(cmd *cobra.Command, root, agent, event string) (gate.Action, gate.Decision, error) {
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
	normalized, err := gate.Normalize(agent, data)
	if err != nil {
		return a, d, err
	}
	normalized.Role = a.Role
	a = normalized
	// Normalize uses the hook cwd; policy paths are relative to the project.
	if (a.Kind == "read" || a.Kind == "write") && a.Target != "" {
		var hook struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal(data, &hook); err != nil {
			return a, d, err
		}
		path := filepath.FromSlash(a.Target)
		if !filepath.IsAbs(path) {
			cwd := hook.Cwd
			if cwd == "" {
				cwd = root
			}
			path = filepath.Join(cwd, path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return a, d, err
		}
		a.Target = filepath.ToSlash(relative)
	}
	cfg, err := config.Load(root)
	if err != nil {
		return a, d, err
	}
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		return a, d, err
	}
	lead, m, err := gate.LeadMode(reg, session.CurrentLead(os.Environ()))
	if err != nil {
		return a, d, err
	}
	d.Mode = m
	rules, err := gate.ImportLead(root, lead.Agent)
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
	return a, gate.Evaluate(cfg.Policy, rules, m, a), nil
}

func newDecisionsCommand() *cobra.Command {
	return &cobra.Command{Use: "decisions", Short: "Show recorded permission decisions and reasons", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(root, ".duolog/decisions.ndjson"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		decoder := json.NewDecoder(f)
		for {
			var row gate.Record
			if err := decoder.Decode(&row); err == io.EOF {
				return nil
			} else if err != nil {
				return err
			}
			label := map[string]string{gate.Allow: "Allow", gate.Deny: "Deny", gate.Ask: "Ask"}[row.Kind]
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s (mode %s; rule %s) Reason: %s\n", label, row.Action.Kind, row.Action.Target, row.Mode, row.Rule, row.Reason); err != nil {
				return err
			}
		}
	}}
}

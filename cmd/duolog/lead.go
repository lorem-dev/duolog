package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

// leadAgentPID finds the agent process above this command's shell. Tests
// replace it to stand in for a process tree.
var leadAgentPID = session.AgentPID

func newLeadCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "lead", Short: "Manage the lead session of this project"}
	cmd.AddCommand(newLeadRegisterCommand())
	return cmd
}

// newLeadRegisterCommand registers the running session as the lead when its
// SessionStart hook did not: the session started in another folder, or was
// resumed here after a restart.
func newLeadRegisterCommand() *cobra.Command {
	var agentFlag string
	cmd := &cobra.Command{
		Use:   "register [plan|ask|edits|full]",
		Short: "Register this Claude or Codex session as the project's lead",
		Long: "Register the Claude or Codex session that runs this command as the lead of the project in the current folder.\n" +
			"Run it from the session's own shell. The mode you give can only be at or below the mode duolog observes in the session's transcript; with no observation it is at most ask.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if isWorker() {
				return errors.New("workers cannot register a lead")
			}
			var requested mode.Mode
			if len(args) > 0 {
				requested = mode.Mode(args[0])
				switch requested {
				case mode.Plan, mode.Ask, mode.Edits, mode.Full:
				default:
					return fmt.Errorf("unknown mode %q; use plan, ask, edits or full", args[0])
				}
			}
			agent, id, err := leadIdentity(agentFlag)
			if err != nil {
				return err
			}
			if agent == "claude" && os.Getenv("CLAUDE_CODE_ENTRYPOINT") != "cli" {
				return errors.New("only an interactive Claude Code session in a terminal can be the lead")
			}
			pid := leadAgentPID()
			if pid <= 1 {
				return fmt.Errorf("cannot find the %s process that runs this shell; run duolog lead register from the session's own shell", agent)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			transcript := session.FindTranscript(agent, id, home)
			var observed session.LeadState
			if transcript != "" {
				observed, _ = session.ReadLeadState(agent, transcript)
			}
			if agent == "claude" {
				observed.Mode = ""
				if transcript != "" {
					observed.Mode = session.ClaudeMode(transcript)
				}
			} else if session.Originator(transcript) != "codex-tui" {
				return fmt.Errorf("cannot find the transcript of interactive Codex thread %s under CODEX_HOME/sessions; only an interactive Codex session can be the lead", id)
			}
			root, err := projectRoot()
			if err != nil {
				return err
			}
			if _, err := store.Open(root); err != nil {
				return err
			}
			reg, err := session.Open(filepath.Join(root, ".duolog"))
			if err != nil {
				return err
			}
			existing, live, err := reg.Lead(id)
			if err != nil {
				return err
			}
			if live && existing.Agent != agent {
				return fmt.Errorf("session %s is registered as a %s lead", id, existing.Agent)
			}
			if live && existing.PID > 0 && existing.PID != pid {
				return fmt.Errorf("session %s is already the live lead under process %d; use duolog mode to lower its mode", id, existing.PID)
			}
			// The ceiling is what duolog observes: the transcript, then a live
			// hook record, then the safe default.
			ceiling := observed.Mode
			if ceiling == "" {
				ceiling = mode.Ask
				if live {
					ceiling = mode.Lower(existing.Mode, existing.Mode)
				}
			}
			if requested != "" && mode.Lower(requested, ceiling) != requested {
				if observed.Mode == "" && !live {
					return fmt.Errorf("cannot register at %s: duolog cannot observe this session's permission mode, so it registers at ask or below", requested)
				}
				return fmt.Errorf("cannot register at %s above the observed mode %s", requested, ceiling)
			}
			// Taken after reg.Lead, which ends a record whose process is gone, so
			// that this observation resumes the session rather than predating its end.
			started := time.Now()
			o := session.LeadObservation{ID: id, Agent: agent, PID: &pid}
			if transcript != "" {
				o.TranscriptPath = &transcript
			}
			if observed.Model != "" {
				o.Model = &observed.Model
			}
			if observed.Effort != "" {
				o.Effort = &observed.Effort
			}
			if observed.Mode != "" {
				o.Mode = &observed.Mode
			}
			lead, err := reg.ObserveLead(o, started)
			if err != nil {
				return err
			}
			if requested != "" {
				if err := reg.SetMode(id, requested); err != nil {
					return err
				}
			}
			_, effective, err := reg.EffectiveMode(id)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s registered as the lead: process %d, mode %s\n", session.Address(lead), pid, effective)
			return err
		},
	}
	cmd.Flags().StringVar(&agentFlag, "agent", "", "The agent of this session, claude or codex, when both are set in the environment")
	return cmd
}

// leadIdentity reads the agent and session ID that the agent exports to its
// shell: CLAUDE_CODE_SESSION_ID for Claude, CODEX_THREAD_ID for Codex.
func leadIdentity(flag string) (agent, id string, err error) {
	claudeID, codexID := os.Getenv("CLAUDE_CODE_SESSION_ID"), os.Getenv("CODEX_THREAD_ID")
	switch flag {
	case "claude":
		agent, id = flag, claudeID
	case "codex":
		agent, id = flag, codexID
	case "":
		switch {
		case claudeID != "" && codexID != "":
			return "", "", errors.New("both CLAUDE_CODE_SESSION_ID and CODEX_THREAD_ID are set; pass --agent claude or --agent codex")
		case claudeID != "":
			agent, id = "claude", claudeID
		case codexID != "":
			agent, id = "codex", codexID
		case os.Getenv("CLAUDECODE") != "":
			agent = "claude"
		case os.Getenv("CODEX_SANDBOX") != "" || os.Getenv("CODEX_SANDBOX_NETWORK_DISABLED") != "":
			agent = "codex"
		default:
			return "", "", errors.New("run duolog lead register inside a Claude Code or Codex session")
		}
	default:
		return "", "", fmt.Errorf("unknown agent %q; use claude or codex", flag)
	}
	if id == "" {
		name := "CLAUDE_CODE_SESSION_ID"
		if agent == "codex" {
			name = "CODEX_THREAD_ID"
		}
		return "", "", fmt.Errorf("this %s session does not set %s, so duolog cannot tell which session to register; start the session in this project's folder so that its hooks register it", agent, name)
	}
	if !session.ValidSessionID(id) {
		return "", "", fmt.Errorf("session ID %q has characters a session ID never has", id)
	}
	return agent, id, nil
}

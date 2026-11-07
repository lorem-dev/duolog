package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/spf13/cobra"
)

func newLeadCommand(d *cmdDeps) *cobra.Command {
	cmd := &cobra.Command{Use: "lead", Short: "Manage the lead session of this project"}
	cmd.AddCommand(newLeadRegisterCommand(d))
	return cmd
}

// newLeadRegisterCommand registers the running session as the lead when its
// SessionStart hook did not: the session started in another folder, or was
// resumed here after a restart.
func newLeadRegisterCommand(d *cmdDeps) *cobra.Command {
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
			root, err := projectRoot()
			if err != nil {
				return err
			}
			res, err := lead.Register(cmd.Context(), d.Lead, lead.RegisterRequest{Agent: agentFlag, Mode: requested, Root: root, Environ: os.Environ()})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s registered as the lead: process %d, mode %s\n", res.Address, res.PID, res.Mode)
			return err
		},
	}
	cmd.Flags().StringVar(&agentFlag, "agent", "", "The agent of this session, "+agentChoice()+", when both are set in the environment")
	return cmd
}

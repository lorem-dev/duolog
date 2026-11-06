package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/lorem-dev/duolog/internal/project"
	"github.com/spf13/cobra"
)

var version = "1.5.0"

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "duolog",
		Short:        "Coordinate Claude Code and Codex tasks",
		SilenceUsage: true,
	}
	cmd.AddCommand(newCalibrateCommand())
	cmd.AddCommand(newCoauthorsCommand())
	cmd.AddCommand(newQuotaCommand())
	cmd.AddCommand(newSkillCommand())
	cmd.AddCommand(&cobra.Command{Use: "version", Short: "Print the duolog version", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
		return err
	}})
	cmd.AddCommand(newConfigCommand(), newRouteCommand(), newSendCommand(), newInboxCommand(), newPutCommand(), newRefCommand(), newReadCommand(), newDelegateCommand(), newDoctorCommand(), newRosterCommand(), newStatsCommand(), newGateCommand(), newDecideCommand("allow"), newDecideCommand("deny"), newRememberCommand(), newModeCommand(), newLeadCommand(), newDecisionsCommand(), newHookCommand(), newSyncCommand(), newInstallCommand(), newUninstallCommand(), newPeerCommand(), newCapsCommand(), newTaskCommand(), newLearnCommand(), newProfileCommand(), newVerdictCommand(), newTelemetryCommand())
	return cmd
}

// projectRoot uses the common Git directory so linked worktrees share settings.
// A directory outside a Git repository uses its working directory as the root.
func projectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return project.StateRoot(cwd), nil
}

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer cancel()
	if err := newRootCommand().ExecuteContext(ctx); err != nil {
		return exitCode(err)
	}
	return 0
}

func workRoot(fallback string) string { return project.CurrentWorkRoot(fallback) }

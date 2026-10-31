package main

import (
	"context"
	"github.com/lorem-dev/duolog/internal/project"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
)

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "duolog",
		Short:        "Coordinate Claude Code and Codex tasks",
		SilenceUsage: true,
	}
	cmd.AddCommand(newConfigCommand(), newRouteCommand(), newSendCommand(), newInboxCommand(), newPutCommand(), newRefCommand(), newReadCommand(), newDelegateCommand(), newDoctorCommand(), newRosterCommand(), newStatsCommand(), newGateCommand(), newDecideCommand("allow"), newDecideCommand("deny"), newModeCommand(), newDecisionsCommand(), newHookCommand(), newSyncCommand(), newInstallCommand(), newUninstallCommand(), newPeerCommand(), newCapsCommand())
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
	ctx, cancel := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer cancel()
	if err := newRootCommand().ExecuteContext(ctx); err != nil {
		os.Exit(exitCode(err))
	}
}

func workRoot(fallback string) string { return project.CurrentWorkRoot(fallback) }

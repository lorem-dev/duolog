package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/spf13/cobra"
)

var version = "1.8.0"

func newRootCommand(d *cmdDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "duolog",
		Short:        "Coordinate Claude Code and Codex tasks",
		SilenceUsage: true,
		Version:      version,
	}
	// --version and -V print the same line as duolog version.
	cmd.Flags().BoolP("version", "V", false, "Print the duolog version")
	cmd.SetVersionTemplate("{{.Version}}\n")
	cmd.AddCommand(newCalibrateCommand(d))
	cmd.AddCommand(newCoauthorsCommand())
	cmd.AddCommand(newQuotaCommand(d))
	cmd.AddCommand(newSkillCommand(d))
	cmd.AddCommand(&cobra.Command{Use: "version", Short: "Print the duolog version", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
		return err
	}})
	cmd.AddCommand(newConfigCommand(), newRouteCommand(), newSendCommand(), newInboxCommand(), newPutCommand(), newRefCommand(), newReadCommand(), newDelegateCommand(d), newDoctorCommand(d), newRosterCommand(d), newStatsCommand(), newGateCommand(d), newDecideCommand("allow", d), newDecideCommand("deny", d), newRememberCommand(), newModeCommand(), newLeadCommand(d), newDecisionsCommand(), newHookCommand(d), newSyncCommand(d), newInstallCommand(d), newUninstallCommand(d), newPeerCommand(d), newRunsCommand(d), newWatchCommand(d), newCapsCommand(), newTaskCommand(), newLearnCommand(), newProfileCommand(d), newVerdictCommand(), newTelemetryCommand(d), newMCPCommand(d))
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
	if err := newRootCommand(newCmdDeps(wiring.Cmd())).ExecuteContext(ctx); err != nil {
		return exitCode(err)
	}
	return 0
}

func workRoot(fallback string) string { return project.CurrentWorkRoot(fallback) }

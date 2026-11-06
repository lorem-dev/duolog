package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/doctor"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/spf13/cobra"
)

var version = "1.4.1"

// The app packages that delegate or open the account gate reach them through
// these variables; they read cmd/duolog's own, which tests replace. Temporary:
// Task 29 replaces them with ports.
func init() {
	profile.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		return runDelegate(ctx, r)
	}
	paidGate := func(cfg *config.Config) (*paidruns.Gate, error) { return delegateDeps.PaidGate(cfg) }
	peer.PaidGate, doctor.PaidGate = paidGate, paidGate
	peer.HeldGate = func(agent, root string, env []string) error {
		return delegate.HeldGate(delegateDeps, agent, root, env)
	}
}

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

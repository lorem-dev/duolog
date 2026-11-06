package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/profile"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

func newProfileCommand() *cobra.Command {
	var entry string
	var opts profile.Options
	cmd := &cobra.Command{Use: "profile <delegation id>", Short: "Replay a delegation with another target in a worktree", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if isWorker() {
			return fmt.Errorf("workers cannot change the journal")
		}
		target, err := config.ParseEntryKey(entry)
		if err != nil {
			return err
		}
		opts.Entry = target.Target()
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		opts.ProtectedRoots = launchProtectedRoots()
		if tel := openTelemetry(cfg, root); tel != nil {
			defer tel.Close()
			opts.Observe = observeAttempt(tel, cfg)
		}
		replay, err := profile.Run(cmd.Context(), cfg, s, args[0], opts)
		if err != nil {
			return err
		}
		ctx := delegate.WithOptions(cmd.Context(), delegate.Options{Lead: opts.Lead, AllowManualModel: opts.AllowManualModel, ProtectedRoots: opts.ProtectedRoots, Observe: opts.Observe})
		verdict, err := profile.Judge(ctx, cfg, s, replay)
		if err != nil {
			return err
		}
		return writeVerdictScreen(cmd.OutOrStdout(), replay, verdict)
	}}
	cmd.Flags().StringVar(&entry, "entry", "", "Replay target as agent/model/effort")
	cmd.Flags().BoolVar(&opts.Chain, "chain", false, "Replay following steps of the same task and class")
	cmd.Flags().BoolVar(&opts.AllowManualModel, "allow-manual-model", false, "Allow a deliberate version pin absent from the live model list")
	cmd.Flags().StringVar(&opts.Lead, "lead", "", "Lead session ID")
	return cmd
}

func writeVerdictScreen(out io.Writer, r profile.Replay, v journal.Verdict) error {
	if _, err := fmt.Fprintf(out, "verdict %s: %s %s, original %s vs profile %s; judge %s, blind, order shuffled\n", v.ID, v.Class, r.Role, v.Original, v.Profile, v.Judge); err != nil {
		return err
	}
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "CRITERION\tORIGINAL\tPROFILE\tREASON"); err != nil {
		return err
	}
	for _, score := range v.Scores {
		if _, err := fmt.Fprintf(table, "%s\t%d\t%d\t%s\n", score.Criterion, score.Original, score.Profile, strings.Join(strings.Fields(score.Reason), " ")); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	passed := func(checks []journal.Check) int {
		n := 0
		for _, check := range checks {
			if check.Exit == 0 {
				n++
			}
		}
		return n
	}
	if _, err := fmt.Fprintf(out, "checks: original %d/%d passed, profile %d/%d passed\ncost: replay $%.4f, judge $%.4f\n", passed(v.OriginalChecks), len(v.OriginalChecks), passed(v.ProfileChecks), len(v.ProfileChecks), v.ReplayUSD, v.JudgeUSD); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Ask the user to confirm or correct. Record it with:\n  duolog verdict %s confirm\n  duolog verdict %s correct \"<criterion>=<original>/<profile>\" ...\n", v.ID, v.ID)
	return err
}

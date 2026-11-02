package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"

	"github.com/lorem-dev/duolog/internal/config"
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
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		replay, err := profile.Run(cmd.Context(), cfg, s, args[0], opts)
		if err != nil {
			return err
		}
		return writeReplaySummary(cmd.OutOrStdout(), replay)
	}}
	cmd.Flags().StringVar(&entry, "entry", "", "Replay target as agent/model/effort")
	cmd.Flags().BoolVar(&opts.Chain, "chain", false, "Replay following steps of the same task and class")
	cmd.Flags().BoolVar(&opts.AllowManualModel, "allow-manual-model", false, "Allow a deliberate version pin absent from the live model list")
	cmd.Flags().StringVar(&opts.Lead, "lead", "", "Lead session ID")
	return cmd
}

func writeReplaySummary(out io.Writer, replay profile.Replay) error {
	for _, item := range []struct {
		name string
		side profile.Side
	}{{"Original", replay.Original}, {"Profile", replay.Profile}} {
		if _, err := fmt.Fprintf(out, "%s: %s ($%.4f)\n%s\n", item.name, item.side.Entry, item.side.USD, item.side.Summary); err != nil {
			return err
		}
		stat := []byte(" 0 files changed\n")
		if len(item.side.Patch) > 0 {
			root, err := projectRoot()
			if err != nil {
				return err
			}
			cmd := exec.Command("git", "apply", "--stat")
			cmd.Dir = root
			cmd.Stdin = bytes.NewReader(item.side.Patch)
			if stat, err = cmd.Output(); err != nil {
				return err
			}
		}
		if _, err := out.Write(stat); err != nil {
			return err
		}
		for _, check := range item.side.Checks {
			if _, err := fmt.Fprintf(out, "  check: %s; exit=%d (%.2fs)\n", check.Command, check.Exit, check.Seconds); err != nil {
				return err
			}
		}
	}
	return nil
}

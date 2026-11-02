package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/learning"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

func newLearnCommand() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{Use: "learn", Short: "Propose a learned model order from the journal", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if apply && isWorker() {
			return fmt.Errorf("workers cannot change the learned order")
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		// Reading an empty journal must not create state, especially for workers.
		j := &journal.Journal{Dir: filepath.Join(root, ".duolog", "journal")}
		ds, err := journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
		if err != nil {
			return err
		}
		ts, err := journal.ReadAll[journal.Task](j, "tasks.ndjson")
		if err != nil {
			return err
		}
		vs, err := journal.ReadAll[journal.Verdict](j, "verdicts.ndjson")
		if err != nil {
			return err
		}
		st := learning.Aggregate(cfg, ds, ts, vs)
		p := learning.Propose(cfg, st, vs)
		if !learning.Changed(cfg, p) {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "no change to the learned order")
			return err
		}
		path := filepath.Join(root, ".duolog", config.LearnedRoutingFile)
		old, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		data, err := learning.Render(p)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if _, err := fmt.Fprintf(out, "proposed .duolog/routing.learned.yaml:\n%sevidence:\n", learning.LineDiff(old, data)); err != nil {
			return err
		}
		var keys []string
		for key, v := range p.Ladders {
			one := config.LearnedRouting{Ladders: map[string]config.LearnedLadder{key: v}}
			if learning.Changed(cfg, one) {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		for _, key := range keys {
			for _, line := range learning.Evidence(st, key) {
				if _, err := fmt.Fprintln(out, line); err != nil {
					return err
				}
			}
		}
		if !apply {
			_, err := fmt.Fprintln(out, "apply with: duolog learn --apply")
			return err
		}
		if _, err := store.Open(root); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0600)
	}}
	cmd.Flags().BoolVar(&apply, "apply", false, "Write the proposed learned order")
	return cmd
}

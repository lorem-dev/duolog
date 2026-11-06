package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/spf13/cobra"
)

func newStatsCommand() *cobra.Command {
	var classes bool
	cmd := &cobra.Command{Use: "stats", Short: "Estimate token costs from usage.ndjson", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		if classes {
			return statsClasses(cmd, root)
		}
		table, err := journal.SummarizeUsage(filepath.Join(root, ".duolog", "usage.ndjson"))
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "ROLE\tAGENT\tMODEL\tINPUT\tCACHED\tCACHE WRITE\tOUTPUT\tUSD (estimated)"); err != nil {
			return err
		}
		for _, row := range table.Rows {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%.6f\n", row.Role, row.Agent, row.Model, row.Input, row.Cached, row.Write, row.Output, row.Cost); err != nil {
				return err
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Total estimated USD: %.6f\n", table.Total); err != nil {
			return err
		}
		for _, warning := range table.Warnings {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "warning:", warning); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&classes, "classes", false, "Summarize learning-mode observations by ladder and entry")
	return cmd
}

func statsClasses(cmd *cobra.Command, root string) error {
	j := &journal.Journal{Dir: filepath.Join(root, ".duolog", "journal")}
	if _, err := os.Stat(j.Dir); os.IsNotExist(err) {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "no journal; learning mode is off or has not run")
		return err
	} else if err != nil {
		return err
	}
	cfg, err := configfile.Load(root)
	if err != nil {
		return err
	}
	ds, ts, vs, err := readLearningJournal(j)
	if err != nil {
		return err
	}
	st := learn.Aggregate(cfg, ds, ts, vs)
	var keys []string
	for key := range st {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "LADDER\tENTRY\tDELEGATIONS\tWEIGHT\tQUALITY\tFAILURES\tUSD\tSECONDS"); err != nil {
		return err
	}
	for _, key := range keys {
		var entries []string
		for entry := range st[key] {
			entries = append(entries, entry)
		}
		slices.Sort(entries)
		for _, entry := range entries {
			s := st[key][entry]
			if _, err := fmt.Fprintf(w, "%s\t%s\t%d\t%g\t%.2f\t%d\t%.2f\t%.0f\n", key, entry, s.Delegations, s.Weight, s.Quality, s.Failures, s.USD, s.Seconds); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}

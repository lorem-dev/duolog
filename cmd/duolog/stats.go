package main

import (
	"fmt"
	"path/filepath"
	"text/tabwriter"

	"github.com/lorem-dev/duolog/internal/usage"
	"github.com/spf13/cobra"
)

func newStatsCommand() *cobra.Command {
	return &cobra.Command{Use: "stats", Short: "Estimate token costs from usage.ndjson", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		table, err := usage.Summarize(filepath.Join(root, ".duolog", "usage.ndjson"))
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "ROLE\tAGENT\tMODEL\tINPUT\tCACHED\tOUTPUT\tUSD (estimated)"); err != nil {
			return err
		}
		for _, row := range table.Rows {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%.6f\n", row.Role, row.Agent, row.Model, row.Input, row.Cached, row.Output, row.Cost); err != nil {
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
}

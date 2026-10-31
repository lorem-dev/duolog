package main

import (
	"fmt"

	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Report Codex folder and hook trust", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		hooks, err := trust.Hooks("codex", root)
		if err != nil {
			return err
		}
		folder, err := trust.Folder("codex", root)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Codex hooks approved=%t folder trusted=%t\nHooks changed after review cannot be detected here; Codex itself will skip changed hooks. Review /hooks.\n", hooks.Trusted, folder)
		return err
	}}
}

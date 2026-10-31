package main

import (
	"fmt"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/spf13/cobra"
)

func newModeCommand() *cobra.Command {
	var leadFlag string
	cmd := &cobra.Command{Use: "mode [plan|ask|edits|full]", Short: "Inspect or lower the lead permission mode", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if isWorker() {
			return fmt.Errorf("workers cannot change the lead mode")
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		reg, err := session.Open(filepath.Join(root, ".duolog"))
		if err != nil {
			return err
		}
		lead, err := reg.ResolveLead(leadFlag)
		if err != nil {
			return err
		}
		id := lead.ID
		if len(args) > 0 {
			if err := reg.SetMode(id, mode.Mode(args[0])); err != nil {
				return err
			}
		}
		_, m, err := reg.EffectiveMode(id)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), m)
		return err
	}}
	cmd.Flags().StringVar(&leadFlag, "lead", "", "Registered lead session ID")
	return cmd
}

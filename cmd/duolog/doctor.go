package main

import (
	"fmt"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/doctor"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	var probe bool
	cmd := &cobra.Command{Use: "doctor", Short: "Report agent health, trust and availability", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		reg, err := session.Open(filepath.Join(root, ".duolog"))
		if err != nil {
			return err
		}
		for _, finding := range doctor.Run(cmd.Context(), cfg, doctor.Options{Root: root, Registry: reg, Probe: probe}) {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", finding.Level, finding.Message); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&probe, "probe", false, "Make a minimal model call from an empty temporary folder")
	return cmd
}

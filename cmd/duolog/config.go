package main

import (
	"github.com/lorem-dev/duolog"
	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect duolog configuration"}
	cmd.AddCommand(&cobra.Command{
		Use:   "schema",
		Short: "Print the JSON schema for configuration files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := cmd.OutOrStdout().Write(duolog.ConfigSchema)
			return err
		},
	})
	return cmd
}

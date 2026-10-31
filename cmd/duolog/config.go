package main

import (
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect duolog configuration"}
	cmd.AddCommand(&cobra.Command{
		Use:   "schema",
		Short: "Print the JSON schema for configuration files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			schema, err := config.Schema()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(schema)
			return err
		},
	})
	return cmd
}

package main

import (
	"encoding/json"
	"fmt"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/spf13/cobra"
)

func newCapsCommand() *cobra.Command {
	var diff, asJSON bool
	cmd := &cobra.Command{Use: "caps", Short: "List skills, plugins and MCP servers per agent", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		var inv []capability.Inventory
		for _, agent := range []string{"claude", "codex"} {
			i, err := capability.Scan(cmd.Context(), agent, root)
			if err != nil {
				return err
			}
			inv = append(inv, i)
		}
		out := cmd.OutOrStdout()
		if diff {
			only := []capability.Inventory{{Agent: inv[0].Agent, Items: capability.Diff(inv[0], inv[1]), Unknown: inv[0].Unknown || inv[1].Unknown}, {Agent: inv[1].Agent, Items: capability.Diff(inv[1], inv[0]), Unknown: inv[0].Unknown || inv[1].Unknown}}
			inv = only
		}
		if asJSON {
			data, err := json.Marshal(inv)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(out, string(data))
			return err
		}
		for _, i := range inv {
			if i.Unknown {
				if _, err := fmt.Fprintf(out, "%s: unknown\n", i.Agent); err != nil {
					return err
				}
				continue
			}
			for _, it := range i.Items {
				if _, err := fmt.Fprintf(out, "%s %s %s\n", i.Agent, it.Kind, it.Name); err != nil {
					return err
				}
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&diff, "diff", false, "Show what each agent has that the other lacks")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

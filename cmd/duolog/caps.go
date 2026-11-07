package main

import (
	"context"
	"encoding/json"
	"fmt"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/wiring"
	"github.com/spf13/cobra"
)

// scanAllCaps lists what every registered agent has, with the project skills
// folders that the project config of the work root sets. The scan reads the
// folders of the work root too, because in a linked worktree those are the
// files the agent loads. A config that does not load keeps the default
// folders, because caps only lists.
func scanAllCaps(ctx context.Context, root string) ([]caps.Inventory, error) {
	work := workRoot(root)
	dirs, err := configfile.ProjectSkillsDirs(work, wiring.Descriptors())
	if err != nil {
		dirs = nil
	}
	var inv []caps.Inventory
	for _, agent := range wiring.Names() {
		i, err := wiring.ScanCaps(ctx, agent, work, dirs)
		if err != nil {
			return nil, err
		}
		inv = append(inv, i)
	}
	return inv, nil
}

func newCapsCommand() *cobra.Command {
	var diff, asJSON bool
	cmd := &cobra.Command{Use: "caps", Short: "List skills, plugins and MCP servers per agent", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		inv, err := scanAllCaps(cmd.Context(), root)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if diff {
			only := []caps.Inventory{{Agent: inv[0].Agent, Items: caps.Diff(inv[0], inv[1]), Unknown: inv[0].Unknown || inv[1].Unknown}, {Agent: inv[1].Agent, Items: caps.Diff(inv[1], inv[0]), Unknown: inv[0].Unknown || inv[1].Unknown}}
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
				// Bridging hands a server to the other agent; see capabilities.on_missing.mcp.
				note := ""
				if it.Bridgeable {
					note = " bridgeable"
				} else if it.NoBridge != "" {
					note = " not bridgeable: " + it.NoBridge
				}
				if _, err := fmt.Fprintf(out, "%s %s %s%s\n", i.Agent, it.Kind, it.Name, note); err != nil {
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

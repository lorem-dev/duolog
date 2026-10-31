package main

import (
	"encoding/json"
	"fmt"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/spf13/cobra"
)

func newRouteCommand() *cobra.Command {
	var asJSON, allowManualModel bool
	cmd := &cobra.Command{
		Use:   "route <role>",
		Short: "Show the agent, model and effort for a role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := projectRoot()
			if err != nil {
				return err
			}
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			route, err := router.Resolve(cfg, args[0], nil, router.Options{AllowManualModel: allowManualModel})
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"agent": route.Agent, "model": route.Model, "effort": route.Effort,
					"sandbox": route.Sandbox, "context": route.Context, "session": route.Session,
					"is_fallback": route.IsFallback, "self": route.Self, "in_lead": route.InLead, "effort_capped": route.EffortCapped,
				})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", route.Agent, route.Model, route.Effort)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the route as JSON")
	cmd.Flags().BoolVar(&allowManualModel, "allow-manual-model", false, "Allow models reserved for manual selection")
	return cmd
}

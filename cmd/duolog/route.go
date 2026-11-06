package main

import (
	"encoding/json"
	"fmt"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/session"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/spf13/cobra"
)

func newRouteCommand() *cobra.Command {
	var asJSON, allowManualModel bool
	var leadFlag string
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
			reg, err := session.Open(filepath.Join(root, ".duolog"))
			if err != nil {
				return err
			}
			lead, err := reg.ResolveLead(leadFlag)
			if err != nil {
				return err
			}
			lead, _, err = gate.LeadMode(reg, lead.ID, time.Now())
			if err != nil {
				return err
			}
			observed, _ := session.ReadLeadState(lead.Agent, lead.TranscriptPath)
			if observed.Model != "" {
				lead.Model = observed.Model
			}
			if observed.Effort != "" {
				lead.Effort = observed.Effort
			}
			unavailable, err := reg.Unavailable()
			if err != nil {
				return err
			}
			opts := router.Options{Lead: &lead.Target, AllowManualModel: allowManualModel,
				Balance: paidruns.DefaultPaceBalance(cmd.Context(), cfg)}
			route, err := router.Resolve(cfg, args[0], unavailable, opts)
			if err != nil {
				return err
			}
			if tel := openAccountTelemetry(cfg, root); tel != nil {
				observeBalance(tel).Route(routeObservation(cfg, args[0], route, opts))
				tel.Close()
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"agent": route.Agent, "model": route.Model, "effort": route.Effort,
					"sandbox": route.Sandbox, "context": route.Context, "session": route.Session,
					"is_fallback": route.IsFallback, "self": route.Self, "in_lead": route.InLead, "effort_capped": route.EffortCapped,
					"reason": route.Reason,
				})
			}
			if _, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s\n", route.Agent, route.Model, route.Effort); err != nil {
				return err
			}
			if route.Reason != "" {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "reason: %s\n", route.Reason)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&leadFlag, "lead", "", "Registered lead session ID")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the route as JSON")
	cmd.Flags().BoolVar(&allowManualModel, "allow-manual-model", false, "Allow models reserved for manual selection")
	return cmd
}

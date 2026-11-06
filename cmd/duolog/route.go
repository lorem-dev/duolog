package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/mode"
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
			opts := router.Options{AllowManualModel: allowManualModel}
			lead, err := reg.ResolveLead(leadFlag)
			// Without a lead, route shows the request; the ceiling needs a lead.
			noLead := err != nil && leadFlag == ""
			var ceiling mode.Ceiling
			switch {
			case noLead:
			case err != nil:
				return err
			default:
				var effective mode.Mode
				if lead, effective, err = gate.LeadMode(reg, lead.ID, time.Now()); err != nil {
					return err
				}
				observed, _ := session.ReadLeadState(lead.Agent, lead.TranscriptPath)
				if observed.Model != "" {
					lead.Model = observed.Model
				}
				if observed.Effort != "" {
					lead.Effort = observed.Effort
				}
				opts.Lead = &lead.Target
				ceiling = mode.NewCeiling(lead.Agent, lead.Native, lead.Mode, effective)
			}
			unavailable, err := reg.Unavailable()
			if err != nil {
				return err
			}
			route, err := router.Resolve(cfg, args[0], unavailable, opts)
			if err != nil {
				return err
			}
			if tel := openAccountTelemetry(cfg, root); tel != nil {
				observeBalance(tel).Route(routeObservation(cfg, args[0], route, opts))
				tel.Close()
			}
			request := cfg.WorkerModes.Request(args[0], config.Families(cfg, route.Model), route.Agent)
			var permission any = mode.Resolve(route.Agent, request, ceiling, route.Sandbox)
			if noLead {
				permission = map[string]string{"request": request.Native, "source": cmp.Or(request.Source, "default"), "ceiling": "no lead"}
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"permission": permission, "agent": route.Agent, "model": route.Model, "effort": route.Effort,
					"sandbox": route.Sandbox, "context": route.Context, "session": route.Session,
					"is_fallback": route.IsFallback, "self": route.Self, "in_lead": route.InLead, "effort_capped": route.EffortCapped,
				})
			}
			line := fmt.Sprintf("%s %s %s", route.Agent, route.Model, route.Effort)
			switch p := permission.(type) {
			case mode.Resolved:
				if p.Source != "default" {
					line += fmt.Sprintf(" mode=%s native=%s (%s)", p.Level, p.Native, p.Source)
				}
			default:
				if request.Native != "" {
					line += fmt.Sprintf(" request=%s (%s);", request.Native, request.Source)
				}
				line += " ceiling: no lead"
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), line)
			return err
		},
	}
	cmd.Flags().StringVar(&leadFlag, "lead", "", "Registered lead session ID")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the route as JSON")
	cmd.Flags().BoolVar(&allowManualModel, "allow-manual-model", false, "Allow models reserved for manual selection")
	return cmd
}

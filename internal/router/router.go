// Package router selects role targets and applies the lead's effort ceiling.
package router

import (
	"fmt"
	"slices"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/models"
)

type Route struct {
	config.Target
	Sandbox      string `json:"sandbox"`
	Context      string `json:"context"`
	Session      string `json:"session"`
	IsFallback   bool   `json:"is_fallback"`
	Self         bool   `json:"self"`
	InLead       bool   `json:"in_lead"`
	EffortCapped bool   `json:"effort_capped"`
}

type Options struct {
	AllowManualModel bool
	Lead             *config.Target
}

// Resolve chooses a configured fallback, or derives one from the model map.
// InLead tells the caller that the unavailable primary moved to the lead's agent;
// Self separately identifies work the lead can perform without a new session.
func Resolve(cfg *config.Config, role string, unavailable map[string]bool, opts Options) (Route, error) {
	spec, ok := cfg.Roles[role]
	if !ok {
		return Route{}, fmt.Errorf("unknown role %q", role)
	}
	route := Route{Target: spec.Use, Sandbox: spec.Sandbox, Context: spec.Context, Session: spec.Session}
	if unavailable[route.Agent] {
		route.IsFallback = true
		route.Target = spec.Fallback
		if route.Agent == "" || route.Agent == spec.Use.Agent || unavailable[route.Agent] || route.Model == "" {
			agent := "claude"
			if spec.Use.Agent == "claude" {
				agent = "codex"
			}
			if unavailable[agent] {
				return Route{}, fmt.Errorf("role %s: no available agent", role)
			}
			model, err := mappedModel(cfg, spec.Use, agent)
			if err != nil {
				return Route{}, fmt.Errorf("role %s: %w", role, err)
			}
			route.Target = config.Target{Agent: agent, Model: model, Effort: spec.Use.Effort}
		}
		if route.Effort == "" {
			route.Effort = spec.Use.Effort
		}
		route.InLead = opts.Lead != nil && route.Agent == opts.Lead.Agent
	}
	alias := models.Normalize(route.Agent, route.Model)
	if !opts.AllowManualModel && (alias == "fable" || alias == "astra" || slices.Contains(cfg.Limits.ManualOnlyModels, alias)) {
		return Route{}, fmt.Errorf("role %s: model %s requires --allow-manual-model", role, route.Model)
	}
	if opts.Lead != nil {
		lead := opts.Lead
		leadAlias := models.Normalize(lead.Agent, lead.Model)
		route.Self = spec.Session == "resume" && route.Agent == lead.Agent && alias == leadAlias
		if spec.Context == "minimal" {
			leadModel, leadOK := cfg.Models[leadAlias]
			roleModel, roleOK := cfg.Models[alias]
			if leadOK && roleOK && leadModel.Input <= roleModel.Input {
				route.Self = true
			}
		}
		efforts := []string{"low", "medium", "high", "xhigh", "max", "ultra"}
		ceiling, requested := slices.Index(efforts, lead.Effort), slices.Index(efforts, route.Effort)
		if ceiling >= 0 && requested > ceiling {
			route.Effort, route.EffortCapped = lead.Effort, true
		}
	}
	return route, nil
}

func mappedModel(cfg *config.Config, primary config.Target, agent string) (string, error) {
	alias := models.Normalize(primary.Agent, primary.Model)
	if primary.Agent == "claude" {
		if name, ok := cfg.Sync.Agents.ModelMap[alias]; ok && name != "" {
			return name, nil
		}
	} else {
		// Refuse ambiguous reverse mappings rather than choosing by map iteration order.
		match := ""
		for claude, codex := range cfg.Sync.Agents.ModelMap {
			if codex == alias {
				if match != "" {
					return "", fmt.Errorf("ambiguous model mapping for %s", alias)
				}
				match = claude
			}
		}
		if match != "" {
			return match, nil
		}
	}
	return "", fmt.Errorf("no model mapping for %s on %s", alias, agent)
}

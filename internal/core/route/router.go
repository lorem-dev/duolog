package route

import (
	"fmt"
	"slices"

	"github.com/lorem-dev/duolog/internal/core/config"
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
	// Reason says why pace balancing moved the task off the role's use agent;
	// empty otherwise.
	Reason string `json:"reason,omitempty"`
	// Ahead is the use agent's pace state after a balance decision, for the
	// caller to keep for the next one.
	Ahead map[string]bool `json:"-"`
}

type Options struct {
	AllowManualModel bool
	Lead             *config.Target
	// Use replaces the role's use target for this call; unavailable agents fall back as for a role.
	Use *config.Target
	// NoSelf forces Self to false so the target runs as a real worker even when it equals the lead.
	NoSelf bool
	// Balance, when set, starts a new task on the role's fallback while its
	// use agent runs ahead of pace. A pinned Use is never balanced.
	Balance *Balance
}

// Resolve chooses a configured fallback, or derives one from the model map.
// InLead tells the caller that the unavailable primary moved to the lead's agent;
// Self separately identifies work the lead can perform without a new session.
func Resolve(cfg *config.Config, role string, unavailable map[string]bool, opts Options) (Route, error) {
	spec, ok := cfg.Roles[role]
	if !ok {
		return Route{}, fmt.Errorf("unknown role %q", role)
	}
	use := spec.Use
	if opts.Use != nil {
		use = *opts.Use
	}
	route := Route{Target: use, Sandbox: spec.Sandbox, Context: spec.Context, Session: spec.Session}
	// A switched-off agent is unavailable, so the role moves to the next
	// enabled agent. A pinned use stays as given and is refused below.
	unavailable = withSwitchedOff(cfg, unavailable, opts.Use)
	if opts.Balance != nil && opts.Use == nil && !unavailable[use.Agent] && !unavailable[spec.Fallback.Agent] {
		d := opts.Balance.Decide(role, spec)
		route.Ahead = d.Ahead
		if d.Move {
			route.Reason = d.Reason
			// Treat the use agent as unavailable for this task, so the
			// fallback path below applies, with the effort defaults.
			unavailable = map[string]bool{use.Agent: true}
		}
	}
	if unavailable[route.Agent] {
		route.IsFallback = true
		route.Target = spec.Fallback
		if route.Agent == "" || route.Agent == use.Agent || unavailable[route.Agent] || route.Model == "" {
			agent := fallbackAgent(cfg, use.Agent, unavailable)
			if agent == "" {
				return Route{}, fmt.Errorf("role %s: no available agent", role)
			}
			model, err := mappedModel(cfg, use, agent)
			if err != nil {
				return Route{}, fmt.Errorf("role %s: %w", role, err)
			}
			route.Target = config.Target{Agent: agent, Model: model, Effort: use.Effort}
		}
		if route.Effort == "" {
			route.Effort = use.Effort
		}
		route.InLead = opts.Lead != nil && route.Agent == opts.Lead.Agent
	}
	alias := Normalize(route.Agent, route.Model)
	if err := refuse(cfg, role, route, alias, opts.AllowManualModel); err != nil {
		return Route{}, err
	}
	if opts.Lead != nil {
		lead := opts.Lead
		leadAlias := Normalize(lead.Agent, lead.Model)
		route.Self = !opts.NoSelf && spec.Session == "resume" && route.Agent == lead.Agent && alias == leadAlias
		if spec.Context == "minimal" {
			leadModel, leadOK := cfg.Models[leadAlias]
			roleModel, roleOK := cfg.Models[alias]
			if !opts.NoSelf && leadOK && roleOK && leadModel.Input <= roleModel.Input {
				route.Self = true
			}
		}
		capEffort(&route, lead.Effort)
	}
	return route, nil
}

// capEffort holds the route's effort to the lead's.
func capEffort(route *Route, leadEffort string) {
	efforts := []string{"low", "medium", "high", "xhigh", "max", "ultra"}
	ceiling, requested := slices.Index(efforts, leadEffort), slices.Index(efforts, route.Effort)
	if ceiling >= 0 && requested > ceiling {
		route.Effort, route.EffortCapped = leadEffort, true
	}
	// With no effort the agent picks its own default, which can be above the lead's.
	if ceiling >= 0 && route.Effort == "" {
		route.Effort = leadEffort
	}
}

// withSwitchedOff copies unavailable and marks every registered agent that is
// switched off, except the agent of a pinned use, which refuse reports.
func withSwitchedOff(cfg *config.Config, unavailable map[string]bool, pinned *config.Target) map[string]bool {
	out := make(map[string]bool, len(unavailable))
	for name, v := range unavailable {
		out[name] = v
	}
	for name := range cfg.Agents {
		if cfg.AgentRegistered(name) && !cfg.AgentEnabled(name) && (pinned == nil || pinned.Agent != name) {
			out[name] = true
		}
	}
	return out
}

// refuse says why a resolved route may not run: its agent is switched off, or
// its model is manual-only and the caller did not allow it.
func refuse(cfg *config.Config, role string, route Route, alias string, allowManual bool) error {
	if route.Agent != "" && cfg.AgentRegistered(route.Agent) && !cfg.AgentEnabled(route.Agent) {
		return fmt.Errorf("role %s: agent %s is switched off (agents.%s.enabled is false)", role, route.Agent, route.Agent)
	}
	if !allowManual && (alias == "fable" || alias == "astra" || slices.Contains(cfg.Limits.ManualOnlyModels, alias)) {
		return fmt.Errorf("role %s: model %s requires --allow-manual-model", role, route.Model)
	}
	return nil
}

// fallbackAgent is the first agent of routing.agent_order that is registered,
// enabled, available and not the use agent; empty when there is none.
func fallbackAgent(cfg *config.Config, use string, unavailable map[string]bool) string {
	order := cfg.Routing.AgentOrder
	if len(order) == 0 {
		order = config.DefaultAgentOrder()
	}
	for _, name := range order {
		if name != use && cfg.AgentRegistered(name) && cfg.AgentEnabled(name) && !unavailable[name] {
			return name
		}
	}
	return ""
}

// mappedModel reads the rows of model_map and model_rows. An ambiguous reverse
// mapping is refused rather than chosen by map iteration order.
func mappedModel(cfg *config.Config, primary config.Target, agent string) (string, error) {
	alias := Normalize(primary.Agent, primary.Model)
	return cfg.Sync.Agents.MapModel(primary.Agent, alias, agent)
}

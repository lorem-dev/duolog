package skilltest

import (
	"fmt"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
	"regexp"
	"slices"
	"strings"
)

// manualOnly reports whether the router refuses alias without
// --allow-manual-model.
func manualOnly(cfg *config.Config, alias string) bool {
	return alias == "fable" || alias == "astra" || slices.Contains(cfg.Limits.ManualOnlyModels, alias)
}

// DefaultTargets takes the price endpoints of each available agent's catalog,
// leaving out manual-only models. Config has prices rather than capability
// ranks; the upper endpoint is the strongest tier. Explicit model lists can
// choose intermediate tiers. agents are the registered agents' names, in
// registry order.
func DefaultTargets(cfg *config.Config, agents []string, unavailable map[string]bool) []config.Target {
	var targets []config.Target
	for _, agent := range agents {
		if unavailable[agent] {
			continue
		}
		var names []string
		for name, m := range cfg.Models {
			if m.Agent == agent && !manualOnly(cfg, name) {
				names = append(names, name)
			}
		}
		slices.SortFunc(names, func(a, b string) int {
			pa, pb := cfg.Models[a], cfg.Models[b]
			if pa.Input+pa.Output < pb.Input+pb.Output {
				return -1
			}
			if pa.Input+pa.Output > pb.Input+pb.Output {
				return 1
			}
			return strings.Compare(a, b)
		})
		if len(names) == 0 {
			continue
		}
		endpoints := []string{names[0]}
		if len(names) > 1 {
			endpoints = append(endpoints, names[len(names)-1])
		}
		for _, name := range endpoints {
			for _, effort := range []string{"low", "high"} {
				targets = append(targets, config.Target{Agent: agent, Model: name, Effort: effort})
			}
		}
	}
	return targets
}

var safeModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ParseTargets reads agent/model/effort runs. A manual-only model needs
// allowManual, as for delegation. agents are the registered agents' names.
func ParseTargets(cfg *config.Config, agents []string, text string, allowManual bool) ([]config.Target, error) {
	var targets []config.Target
	for _, entry := range strings.Split(text, ",") {
		parts := strings.Split(strings.TrimSpace(entry), "/")
		if len(parts) != 3 || !safeModel.MatchString(parts[1]) {
			return nil, fmt.Errorf("model must be agent/model/effort: %q", entry)
		}
		t := config.Target{Agent: parts[0], Model: parts[1], Effort: parts[2]}
		if !slices.Contains(agents, t.Agent) {
			return nil, fmt.Errorf("unknown agent %q", t.Agent)
		}
		if !slices.Contains([]string{"low", "medium", "high", "xhigh", "max", "ultra"}, t.Effort) {
			return nil, fmt.Errorf("unknown effort %q", t.Effort)
		}
		alias := route.Normalize(t.Agent, t.Model)
		price, ok := cfg.Models[alias]
		if !ok || price.Agent != t.Agent {
			return nil, fmt.Errorf("no configured price for %s/%s", t.Agent, t.Model)
		}
		if !allowManual && manualOnly(cfg, alias) {
			return nil, fmt.Errorf("model %s requires --allow-manual-model", t.Model)
		}
		if !slices.Contains(targets, t) {
			targets = append(targets, t)
		}
	}
	return targets, nil
}

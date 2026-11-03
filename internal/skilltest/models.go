package skilltest

import (
	"fmt"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/models"
	"regexp"
	"slices"
	"strings"
)

// DefaultTargets takes the price endpoints of each available agent's catalog.
// Config has prices rather than capability ranks; the upper endpoint is the
// strongest tier. Explicit model lists can choose intermediate tiers.
func DefaultTargets(cfg *config.Config, unavailable map[string]bool) []config.Target {
	var targets []config.Target
	for _, agent := range []string{"claude", "codex"} {
		if unavailable[agent] {
			continue
		}
		var names []string
		for name, m := range cfg.Models {
			if m.Agent == agent {
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

func ParseTargets(cfg *config.Config, text string) ([]config.Target, error) {
	var targets []config.Target
	for _, entry := range strings.Split(text, ",") {
		parts := strings.Split(strings.TrimSpace(entry), "/")
		if len(parts) != 3 || !safeModel.MatchString(parts[1]) {
			return nil, fmt.Errorf("model must be agent/model/effort: %q", entry)
		}
		t := config.Target{Agent: parts[0], Model: parts[1], Effort: parts[2]}
		if t.Agent != "claude" && t.Agent != "codex" {
			return nil, fmt.Errorf("unknown agent %q", t.Agent)
		}
		if !slices.Contains([]string{"low", "medium", "high", "xhigh", "max", "ultra"}, t.Effort) {
			return nil, fmt.Errorf("unknown effort %q", t.Effort)
		}
		price, ok := cfg.Models[models.Normalize(t.Agent, t.Model)]
		if !ok || price.Agent != t.Agent {
			return nil, fmt.Errorf("no configured price for %s/%s", t.Agent, t.Model)
		}
		if !slices.Contains(targets, t) {
			targets = append(targets, t)
		}
	}
	return targets, nil
}

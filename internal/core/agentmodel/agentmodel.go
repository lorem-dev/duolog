// Package agentmodel maps the models and efforts of an agent file between the
// agents' vocabularies, for duolog sync.
package agentmodel

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/agentfile"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
)

// ToCodex maps the name, model and effort of a Claude agent to the form of its
// Codex file. A Codex alias is resolved to a concrete model through o.Resolve;
// a pinned gpt- model is kept.
func ToCodex(o agentfile.AgentOpts, name, model, effort string) (codexName, codexModel, codexEffort string, err error) {
	target, effort, err := MapModel(o.Config, name, model, effort, "claude", o.Flags)
	if err != nil {
		return "", "", "", err
	}
	if target != "" && !strings.HasPrefix(target, "gpt-") {
		if o.Resolve == nil {
			return "", "", "", errors.New("no model resolver for Codex aliases")
		}
		if target, err = o.Resolve(target); err != nil {
			return "", "", "", fmt.Errorf("resolve Codex model: %w", err)
		}
	}
	codexName, err = agentfile.CodexName(o.Config.CodexName, name)
	if err != nil {
		return "", "", "", err
	}
	return codexName, target, effort, nil
}

// MapModel maps a model and an effort from the vendor named by from ("claude"
// or "codex") to the other one. The model returned is a target alias or a
// deliberate pin; the caller resolves a Codex alias to a concrete name.
// Precedence from Claude to Codex: flags, overrides for the agent, model_map.
func MapModel(cfg config.AgentSync, name, model, effort, from string, flags map[string]string) (string, string, error) {
	if from != "claude" && from != "codex" {
		return "", "", fmt.Errorf("unknown source agent %q", from)
	}
	forward := map[string]string{}
	for k, v := range cfg.ModelMap {
		forward[k] = v
	}
	if err := checkBijective(forward); err != nil {
		return "", "", err
	}
	if from == "codex" {
		// Flags pick a Claude alias -> Codex alias, so they apply only from
		// Claude to Codex. Merging them here would break the inverse map.
		// A pinned Codex model returns as the latest of its family: the pin
		// is lost on a round trip through Claude (lossy by design).
		return mapToClaude(cfg, forward, model, effort)
	}
	merged := make(map[string]string, len(forward)+len(flags))
	for k, v := range forward {
		merged[k] = v
	}
	for k, v := range flags {
		merged[k] = v
	}
	return mapToCodex(cfg, merged, flags, name, model, effort)
}

func checkBijective(m map[string]string) error {
	seen := map[string]string{}
	for _, k := range agentfile.SortedKeys(m) {
		if other, dup := seen[m[k]]; dup {
			return fmt.Errorf("model_map is not one to one: %q and %q both map to %q", other, k, m[k])
		}
		seen[m[k]] = k
	}
	return nil
}

func mapToCodex(cfg config.AgentSync, forward, flags map[string]string, name, model, effort string) (string, string, error) {
	alias := ""
	if model != "" {
		alias = route.Normalize("claude", model)
		switch override := cfg.Overrides[name]; {
		case flags[alias] != "":
			alias = flags[alias]
		case override.Model != "":
			alias = override.Model
		case forward[alias] != "":
			alias = forward[alias]
		default:
			return "", "", fmt.Errorf("model %q is not in model_map; known Claude models: %s", model, strings.Join(agentfile.SortedKeys(forward), ", "))
		}
	}
	if override := cfg.Overrides[name].Effort; override != "" {
		if !knownEffort(cfg.EffortMap, override) {
			return "", "", fmt.Errorf("effort %q (override for %s) is not in effort_map", override, name)
		}
		return alias, override, nil
	}
	if effort == "" {
		if def := cfg.DefaultEffort[route.Normalize("claude", model)]; def != "" && model != "" {
			effort = def
		}
	}
	if effort != "" && !knownEffort(cfg.EffortMap, effort) {
		return "", "", fmt.Errorf("effort %q is not in effort_map", effort)
	}
	return alias, effort, nil
}

func knownEffort(effortMap map[string]string, effort string) bool {
	if _, ok := effortMap[effort]; ok {
		return true
	}
	for _, v := range effortMap {
		if v == effort {
			return true
		}
	}
	return false
}

func mapToClaude(cfg config.AgentSync, forward map[string]string, model, effort string) (string, string, error) {
	if model != "" && !strings.HasPrefix(model, "claude-") {
		alias := route.Normalize("codex", model)
		found := ""
		for _, k := range agentfile.SortedKeys(forward) {
			if forward[k] == alias {
				found = k
			}
		}
		if found == "" {
			known := make([]string, 0, len(forward))
			for _, v := range forward {
				known = append(known, v)
			}
			sort.Strings(known)
			return "", "", fmt.Errorf("model %q is not in model_map; known Codex models: %s", model, strings.Join(known, ", "))
		}
		model = found
	}
	if effort != "" {
		mapped, ok := cfg.EffortMap[effort]
		if !ok {
			return "", "", fmt.Errorf("effort %q is not in effort_map", effort)
		}
		effort = mapped
	}
	return model, effort, nil
}

package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// LoadOptions is what Load needs to know about the registered agents. wiring
// builds it from the registry.
type LoadOptions struct {
	// Agents are the registered agents' descriptors, in registry order.
	Agents []agent.Descriptor
}

// builtinAgents stands for the registry where Load gets no options: the tests
// of this layer and the security boundary's callers, whose function bodies may
// not change in this task. It carries only the facts that Load reads, and
// goes when those callers pass wiring.ConfigOptions.
func builtinAgents() []agent.Descriptor {
	return []agent.Descriptor{
		{Name: "claude", Display: "Claude", Config: agent.ConfigFacts{SubscriptionEndpoints: true}},
		{Name: "codex", Display: "Codex"},
	}
}

// agentsOf returns the descriptors Load works with: the options' agents, or
// the built-in ones.
func agentsOf(opts []LoadOptions) []agent.Descriptor {
	for _, o := range opts {
		if len(o.Agents) > 0 {
			return o.Agents
		}
	}
	return builtinAgents()
}

// descriptorOf returns the descriptor called name, the zero one when none is.
func descriptorOf(agents []agent.Descriptor, name string) agent.Descriptor {
	for _, a := range agents {
		if a.Name == name {
			return a
		}
	}
	return agent.Descriptor{}
}

// mappingEntry returns the value of key in a mapping node, or nil.
func mappingEntry(node *yaml.Node, key string) *yaml.Node {
	if node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return mappingValue(node, key)
}

func agentNames(agents []agent.Descriptor) []string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return names
}

// agentKeyType is the type of an agent key of a flat agent map, or nil when
// typ has none: paid_runs, coauthors and a worker mode hold one key per agent
// beside their own keys.
func agentKeyType(typ reflect.Type) reflect.Type {
	switch typ {
	case reflect.TypeFor[config.PaidRuns]():
		return reflect.TypeFor[config.PaidAgent]()
	case reflect.TypeFor[config.Coauthors](), reflect.TypeFor[config.WorkerMode]():
		return reflect.TypeFor[string]()
	}
	return nil
}

// validateAgentMaps checks the agent maps of the merged config.
func validateAgentMaps(cfg *config.Config, agents []agent.Descriptor) error {
	if err := validateAgents(cfg, agents); err != nil {
		return err
	}
	return validatePaidRuns(cfg, agents)
}

// validateAgents finishes the agent maps: every registered agent has a switch,
// off when nothing switched it on, an agent's paid settings that name no
// account take auto, and the model rows name registered agents and agree with
// model_map. An agent in routing.agent_order that is not registered is
// skipped when a fallback is chosen, not refused here.
func validateAgents(cfg *config.Config, agents []agent.Descriptor) error {
	if cfg.Agents == nil {
		cfg.Agents = map[string]config.AgentSwitch{}
	}
	for _, a := range agents {
		s := cfg.Agents[a.Name]
		if s.Enabled == nil {
			off := false
			s.Enabled = &off
		}
		cfg.Agents[a.Name] = s
		if p, ok := cfg.PaidRuns.Agents[a.Name]; ok && p.Account == "" {
			p.Account = "auto"
			cfg.PaidRuns.Agents[a.Name] = p
		}
	}
	names := agentNames(agents)
	for i, row := range cfg.Sync.Agents.ModelRows {
		for _, key := range slices.Sorted(maps.Keys(row)) {
			if !slices.Contains(names, key) {
				return fmt.Errorf("sync.agents.model_rows[%d].%s: unknown key", i, key)
			}
		}
	}
	return cfg.Sync.Agents.CheckRows()
}

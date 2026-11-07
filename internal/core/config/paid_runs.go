package config

import (
	"fmt"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"
)

// PaidAgent is one agent's paid-run opt-in. Paid runs are off by default;
// allow needs a positive daily cap, and calibration on a paid route needs
// calibrate as well.
type PaidAgent struct {
	Allow       bool     `yaml:"allow" jsonschema_description:"Allow paid runs for this agent's workers and peers once the subscription cannot serve them; defaults to false. Needs daily_cap_usd above 0."`
	DailyCapUSD float64  `yaml:"daily_cap_usd" jsonschema_description:"Most USD paid runs of this agent may spend in the last 24 hours; defaults to 0 and must be above 0 with allow." jsonschema:"minimum=0"`
	Calibrate   bool     `yaml:"calibrate" jsonschema_description:"Allow calibration on a paid route; defaults to false and needs allow."`
	Env         []string `yaml:"env" jsonschema_description:"Variables passed to this agent's processes only when paid runs are allowed; defaults to the agent's API key."`
	Account     string   `yaml:"account" jsonschema_description:"Account kind: workspace uses usable_share of each window; auto and personal use whole windows only while the quota read and the runs show no workspace evidence, which always wins; defaults to auto." jsonschema:"enum=auto,enum=personal,enum=workspace"`
	// SubscriptionEndpoints is read for the agents whose descriptor says they
	// have endpoints (agent.ConfigFacts.SubscriptionEndpoints); the schema
	// lists it only there.
	SubscriptionEndpoints map[string]string `yaml:"subscription_endpoints,omitempty" jsonschema_description:"Named base URLs that count as the subscription, such as a gateway in front of the claude.ai login; defaults to an empty map."`
}

// PaidRuns holds the paid-run settings. In YAML the agents sit beside
// usable_share, one key per agent: "one key per agent: claude, codex, ...".
type PaidRuns struct {
	UsableShare float64 `yaml:"usable_share" jsonschema_description:"Share of each window duolog uses on a workspace account, above 0 and at most 1; defaults to 0.5." jsonschema:"exclusiveMinimum=0,maximum=1"`
	// Agents is the settings by agent name.
	Agents map[string]PaidAgent `yaml:"-" jsonschema:"-"`
}

// ConfigNotice names a key that Load ignored or capped, and why: a key of a
// shared layer, .duolog/config.yaml or a .duolog/config.local.yaml that Git
// tracks, that would widen a limit or lower a price, or a .duolog layer, shared
// or local, that sets the total or a session tier above its default, which
// only the user config may do.
type ConfigNotice struct {
	Key, Reason string
}

// Agent returns the settings of an agent, and the zero value, which allows
// nothing, for a name that has none.
func (p PaidRuns) Agent(agent string) PaidAgent {
	return p.Agents[agent]
}

// Set stores an agent's settings.
func (p *PaidRuns) Set(agent string, a PaidAgent) {
	if p.Agents == nil {
		p.Agents = map[string]PaidAgent{}
	}
	p.Agents[agent] = a
}

// UnmarshalYAML reads usable_share and one mapping per agent. Which agent
// names are valid is checked against the registry when Load reads a layer.
func (p *PaidRuns) UnmarshalYAML(node *yaml.Node) error {
	return decodeFlat(node, "paid_runs", func(key string, value *yaml.Node) error {
		if key == "usable_share" {
			return value.Decode(&p.UsableShare)
		}
		if p.Agents == nil {
			p.Agents = map[string]PaidAgent{}
		}
		a := p.Agents[key]
		if err := value.Decode(&a); err != nil {
			return err
		}
		p.Agents[key] = a
		return nil
	})
}

// MarshalYAML writes usable_share, then each agent in name order.
func (p PaidRuns) MarshalYAML() (any, error) {
	out := &yaml.Node{Kind: yaml.MappingNode}
	if err := addKey(out, "usable_share", p.UsableShare); err != nil {
		return nil, err
	}
	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		if err := addKey(out, name, p.Agents[name]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// decodeFlat calls set for each key of a mapping node.
func decodeFlat(node *yaml.Node, what string, set func(key string, value *yaml.Node) error) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: %s must be a mapping", node.Line, what)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if err := set(node.Content[i].Value, node.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// addKey appends a key and its encoded value to a mapping node.
func addKey(out *yaml.Node, key string, v any) error {
	var value yaml.Node
	if err := value.Encode(v); err != nil {
		return err
	}
	out.Content = append(out.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &value)
	return nil
}

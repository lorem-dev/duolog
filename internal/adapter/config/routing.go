package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// restrictProjectAgents removes each agents.<name>.enabled: true from a shared
// layer: it may switch an agent off, never on, because an agent the person did
// not enable would otherwise receive the project's code as a fallback. A true
// the layers below already have is kept: it widens nothing. The nodes are
// resolved mappings.
func restrictProjectAgents(base, layer *yaml.Node, projectFile string) ([]config.ConfigNotice, error) {
	agents := mappingValue(layer, "agents")
	if agents == nil || agents.Kind != yaml.MappingNode {
		return nil, nil
	}
	var notices []config.ConfigNotice
	for i := 0; i+1 < len(agents.Content); i += 2 {
		name, entry := agents.Content[i].Value, agents.Content[i+1]
		if entry.Kind != yaml.MappingNode {
			continue
		}
		var err error
		entry.Content, err = keepEntries(entry.Content, func(key string, value *yaml.Node) (bool, error) {
			if key != "enabled" {
				return true, nil
			}
			var on bool
			if err := decodeOptional(value, &on); err != nil {
				return false, err
			}
			if !on {
				return true, nil
			}
			if baseOn, err := baseAgentOn(base, name); err != nil || baseOn {
				return baseOn, err
			}
			notices = append(notices, config.ConfigNotice{Key: "agents." + name + ".enabled", Reason: fmt.Sprintf(
				"true in %s is ignored: a shared config may only switch an agent off", projectFile)})
			return false, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return notices, nil
}

// baseAgentOn reports whether the layers below have agents.<name>.enabled on;
// a missing value counts as off.
func baseAgentOn(base *yaml.Node, name string) (bool, error) {
	var on bool
	err := decodeOptional(mappingValue(mappingValue(mappingValue(base, "agents"), name), "enabled"), &on)
	return on, err
}

// restrictProjectRouting removes the project's routing.balance when it turns
// balancing off under a layer that has it on. The margin is bounded with the
// other account limits. The nodes are resolved mappings.
func restrictProjectRouting(base, layer *yaml.Node, projectFile string) ([]config.ConfigNotice, error) {
	value, err := limitNode(layer, "routing.balance")
	if err != nil || value == nil {
		return nil, err
	}
	baseValue, err := limitNode(base, "routing.balance")
	if err != nil {
		return nil, err
	}
	if value.Value != "off" || baseValue == nil || baseValue.Value == "off" {
		return nil, nil
	}
	parent, _ := limitNode(layer, "routing")
	parent.Content, _ = keepEntries(parent.Content, func(key string, _ *yaml.Node) (bool, error) {
		return key != "balance", nil
	})
	return []config.ConfigNotice{{Key: "routing.balance", Reason: fmt.Sprintf(
		"off in %s is ignored: a shared config may not turn pace balancing off; %s applies", projectFile, baseValue.Value)}}, nil
}

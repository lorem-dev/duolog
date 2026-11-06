package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/config"
)

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

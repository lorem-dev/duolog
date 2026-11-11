// Package agentplugin holds the note and the marketplace list reading for
// installing the duolog plugin from its marketplace. Each agent's adapter
// names its own commands; nothing here runs them.
package agentplugin

import (
	"encoding/json"
	"strings"
)

// PluginMissingNote names an absent duolog plugin of agent and the exact
// commands, each an argument list after the agent's own program, that
// install it, shared by install --check, install and doctor.
func PluginMissingNote(agent string, commands [][]string) string {
	steps := make([]string, 0, len(commands))
	for _, c := range commands {
		steps = append(steps, agent+" "+strings.Join(c, " "))
	}
	return agent + ": duolog plugin is not installed; run " + strings.Join(steps, "; then ")
}

// HasMarketplace matches a complete marketplace name in the CLI list, in
// plain or JSON output. A source path or a longer name does not count.
func HasMarketplace(data []byte, name string) bool {
	var doc any
	if json.Unmarshal(data, &doc) == nil {
		return marketplaceNamed(doc, name)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "-\u2022\u276f ")
		words := strings.Fields(line)
		if len(words) > 0 && strings.TrimSuffix(words[0], ":") == name {
			return true
		}
		if len(words) > 1 && strings.EqualFold(words[0], "name:") && words[1] == name {
			return true
		}
	}
	return false
}

// marketplaceNamed finds an exact name in JSON list or keyed registry output.
// Other string fields, such as source paths and descriptions, do not count.
func marketplaceNamed(value any, name string) bool {
	switch v := value.(type) {
	case []any:
		for _, entry := range v {
			if entry == name || marketplaceNamed(entry, name) {
				return true
			}
		}
	case map[string]any:
		if v["name"] == name {
			return true
		}
		if _, ok := v[name]; ok {
			return true
		}
		for _, entry := range v {
			if marketplaceNamed(entry, name) {
				return true
			}
		}
	}
	return false
}

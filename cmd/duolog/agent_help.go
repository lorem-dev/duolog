package main

import (
	"strings"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// loadConfig loads the config of the project at root over the registered
// agents.
func loadConfig(root string) (*config.Config, error) {
	return configfile.Load(root, wiring.ConfigOptions())
}

// agentFlagHelp is the help of an --agent flag that also takes extra, such as
// "both": "Which agent: claude, codex or both" while two agents are registered.
func agentFlagHelp(extra string) string {
	return "Which agent: " + strings.Join(wiring.Names(), ", ") + " or " + extra
}

// agentChoice names the registered agents for a sentence: "claude or codex"
// for two, "claude, codex or kestrel" for three.
func agentChoice() string {
	names := wiring.Names()
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

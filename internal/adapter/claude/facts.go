// Package claude holds everything that is particular to Claude Code.
package claude

import "github.com/lorem-dev/duolog/internal/core/agent"

// Facts describes Claude Code.
var Facts = agent.Descriptor{
	Name:          "claude",
	Display:       "Claude",
	Product:       "Claude Code",
	Binaries:      []string{"claude"},
	ConfigEnv:     "CLAUDE_CONFIG_DIR",
	ConfigDefault: ".claude",
	ProjectDir:    ".claude",
	SessionEnv:    []string{"CLAUDE_CODE_SESSION_ID"},
	Markers:       []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"},
	ModelPrefix:   "claude-",
	Families:      []string{"fable", "opus", "sonnet", "haiku"},
	Credentials: agent.EnvNames{
		APIKey:    []string{"ANTHROPIC_API_KEY"},
		AuthToken: []string{"ANTHROPIC_AUTH_TOKEN"},
		BaseURL:   []string{"ANTHROPIC_BASE_URL"},
	},
	HiddenPrefixes:  []string{"ANTHROPIC_", "CLAUDE_"},
	WorkerEnv:       []string{"DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"},
	LoginPhrases:    []string{"not logged in", "please run /login", "invalid api key", "oauth token has expired", "authentication_error", "401 unauthorized"},
	InstructionFile: "CLAUDE.md",
	LeadCanAsk:      true,
	Config:          agent.ConfigFacts{SubscriptionEndpoints: true},
}

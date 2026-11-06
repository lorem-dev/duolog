// Package codex holds everything that is particular to Codex.
package codex

import "github.com/lorem-dev/duolog/internal/core/agent"

// Facts describes Codex.
var Facts = agent.Descriptor{
	Name:          "codex",
	Display:       "Codex",
	Product:       "Codex",
	Binaries:      []string{"codex", "codex-*"},
	Helpers:       []string{"codex-linux-sandbox"},
	ConfigEnv:     "CODEX_HOME",
	ConfigDefault: ".codex",
	ProjectDir:    ".codex",
	SessionEnv:    []string{"CODEX_THREAD_ID"},
	Markers:       []string{"CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "CODEX_THREAD_ID"},
	ModelPrefix:   "gpt-",
	Families:      []string{"astra", "sol", "terra", "luna"},
	Credentials: agent.EnvNames{
		APIKey:  []string{"CODEX_API_KEY", "OPENAI_API_KEY"},
		BaseURL: []string{"OPENAI_BASE_URL"},
	},
	HiddenPrefixes:  []string{"CODEX_", "OPENAI_"},
	LoginPhrases:    []string{"codex login", "token_expired", "invalid_token", "refresh token"},
	InstructionFile: "AGENTS.md",
	Config:          agent.ConfigFacts{SandboxRoots: true},
}

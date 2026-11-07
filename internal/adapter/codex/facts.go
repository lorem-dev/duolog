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
	Pane: agent.PaneFacts{
		Placeholder: `^Ask Codex to do anything$`,
		Updater:     []string{"updating codex"},
		Interrupt:   []string{`^[\s■•]*Conversation interrupted\b`},
		Composer:    agent.ComposerFacts{Marker: "›"},
		// What the pane picks when the gate allows or denies a command.
		Answers:         []agent.AutoAnswer{{Verdict: "allow", Label: "Yes, proceed"}, {Verdict: "deny", Label: "No"}},
		CommandQuestion: "Would you like to run the following command?",
		Queued:          "shift+left to answer",
	},
	Config: agent.ConfigFacts{
		SandboxRoots:  true,
		SkillsDir:     DefaultSkillsDir,
		CoauthorDoc:   "Codex co-author template with {name} and {model}; defaults to OpenAI Codex, the model and noreply@openai.com.",
		NativeModes:   []string{"read-only", "workspace-write"},
		NativeModeDoc: "Native Codex sandbox a worker starts in; danger-full-access is not offered. Absent means duolog's default.",
	},
}

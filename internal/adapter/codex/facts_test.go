package codex

import (
	"reflect"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
)

// TestFacts checks every field of Codex's descriptor.
func TestFacts(t *testing.T) {
	want := agent.Descriptor{
		Name:    "codex",
		Display: "Codex",
		Product: "Codex",
		// internal/session/register.go lists the helper process.
		Binaries:      []string{"codex", "codex-*"},
		Helpers:       []string{"codex-linux-sandbox"},
		ConfigEnv:     "CODEX_HOME",
		ConfigDefault: ".codex",
		ProjectDir:    ".codex",
		// The thread, then the root thread a subagent's thread belongs to.
		SessionEnv: []string{"CODEX_THREAD_ID", "CODEX_SESSION_ID"},
		// cmd/duolog/telemetry.go modelMarkers, Codex's part.
		Markers:     []string{"CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "CODEX_THREAD_ID"},
		ModelPrefix: "gpt-",
		// internal/telemetry/validate.go families, Codex's part.
		Families: []string{"astra", "sol", "terra", "luna"},
		Credentials: agent.EnvNames{
			APIKey:  []string{"CODEX_API_KEY", "OPENAI_API_KEY"},
			BaseURL: []string{"OPENAI_BASE_URL"},
		},
		// internal/adapter/kit/caps.go AgentVariable, Codex's part.
		HiddenPrefixes: []string{"CODEX_", "OPENAI_"},
		// internal/runner/runner.go authFailure, Codex's part.
		LoginPhrases:    []string{"codex login", "token_expired", "invalid_token", "refresh token"},
		InstructionFile: "AGENTS.md",
		LeadCanAsk:      false,
		// One codex app-server process runs every thread.
		OneSessionPerProcess: false,
		// internal/core/screen/screen.go interruptedRe, updatingRe and the
		// Codex arms of Parse; internal/app/peer/auto_answer.go.
		Pane: agent.PaneFacts{
			Placeholder:     `^Ask Codex to do anything$`,
			Updater:         []string{"updating codex"},
			Interrupt:       []string{`^[\s■•]*Conversation interrupted\b`},
			Composer:        agent.ComposerFacts{Marker: "›"},
			Answers:         []agent.AutoAnswer{{Verdict: "allow", Label: "Yes, proceed"}, {Verdict: "deny", Label: "No"}},
			CommandQuestion: "Would you like to run the following command?",
			Queued:          "shift+left to answer",
		},
		Config: agent.ConfigFacts{
			SandboxRoots:  true,
			SkillsDir:     ".agents/skills",
			CoauthorDoc:   "Codex co-author template with {name} and {model}; defaults to OpenAI Codex, the model and noreply@openai.com.",
			NativeModes:   []string{"read-only", "workspace-write"},
			NativeModeDoc: "Native Codex sandbox a worker starts in; danger-full-access is not offered. Absent means duolog's default.",
		},
	}
	if !reflect.DeepEqual(Facts, want) {
		t.Fatalf("Facts = %+v, want %+v", Facts, want)
	}
}

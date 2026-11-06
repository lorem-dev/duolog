package codex

import (
	"reflect"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
)

func TestFacts(t *testing.T) {
	want := agent.Descriptor{
		Name:    "codex",
		Display: "Codex",
		// internal/session/register.go lists the helper process.
		Binaries:      []string{"codex", "codex-*"},
		Helpers:       []string{"codex-linux-sandbox"},
		ConfigEnv:     "CODEX_HOME",
		ConfigDefault: ".codex",
		ProjectDir:    ".codex",
		SessionEnv:    []string{"CODEX_THREAD_ID"},
		// cmd/duolog/telemetry.go modelMarkers, Codex's part.
		Markers:     []string{"CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "CODEX_THREAD_ID"},
		ModelPrefix: "gpt-",
		// internal/telemetry/validate.go families, Codex's part.
		Families: []string{"astra", "sol", "terra", "luna"},
		Credentials: agent.EnvNames{
			APIKey:  []string{"CODEX_API_KEY", "OPENAI_API_KEY"},
			BaseURL: []string{"OPENAI_BASE_URL"},
		},
		// internal/capability/bridge.go agentVariable, Codex's part.
		HiddenPrefixes: []string{"CODEX_", "OPENAI_"},
		// internal/runner/runner.go authFailure, Codex's part.
		LoginPhrases:    []string{"codex login", "token_expired", "invalid_token", "refresh token"},
		InstructionFile: "AGENTS.md",
		LeadCanAsk:      false,
		Config:          agent.ConfigFacts{SandboxRoots: true},
	}
	if !reflect.DeepEqual(Facts, want) {
		t.Fatalf("Facts = %+v, want %+v", Facts, want)
	}
}

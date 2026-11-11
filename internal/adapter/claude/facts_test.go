package claude

import (
	"reflect"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
)

func TestFacts(t *testing.T) {
	want := agent.Descriptor{
		Name:          "claude",
		Display:       "Claude",
		Product:       "Claude Code",
		Binaries:      []string{"claude"},
		Scripts:       []string{"@anthropic-ai/claude-code/cli.js"},
		ConfigEnv:     "CLAUDE_CONFIG_DIR",
		ConfigDefault: ".claude",
		ProjectDir:    ".claude",
		SessionEnv:    []string{"CLAUDE_CODE_SESSION_ID"},
		// cmd/duolog/telemetry.go modelMarkers, Claude's part.
		Markers:     []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"},
		ModelPrefix: "claude-",
		// internal/telemetry/validate.go families, Claude's part.
		Families: []string{"fable", "opus", "sonnet", "haiku"},
		Credentials: agent.EnvNames{
			APIKey:    []string{"ANTHROPIC_API_KEY"},
			AuthToken: []string{"ANTHROPIC_AUTH_TOKEN"},
			BaseURL:   []string{"ANTHROPIC_BASE_URL"},
		},
		// internal/adapter/kit/caps.go AgentVariable, Claude's part.
		HiddenPrefixes: []string{"ANTHROPIC_", "CLAUDE_"},
		// internal/workerenv/process.go:190.
		WorkerEnv: []string{"DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"},
		// internal/runner/runner.go authFailure, Claude's part.
		LoginPhrases:    []string{"not logged in", "please run /login", "invalid api key", "oauth token has expired", "authentication_error", "401 unauthorized"},
		InstructionFile: "CLAUDE.md",
		LeadCanAsk:      true,
		// internal/core/screen/screen.go claudeTry, interruptedRe, updatingRe.
		Pane: agent.PaneFacts{
			Placeholder: `^Try[\s\x{00a0}]+["“]`,
			Updater:     []string{"updating claude"},
			Interrupt:   []string{`^[\s⎿│]*Interrupted(?:\s*·\s*What should Claude do instead\?|\s+by user\b)`},
			Composer:    agent.ComposerFacts{Marker: "❯", Boxed: true},
		},
		Config: agent.ConfigFacts{
			SubscriptionEndpoints: true,
			CoauthorDoc:           "Claude co-author template with {name} and {model}; defaults to a display name and noreply@anthropic.com.",
			NativeModes:           []string{"plan", "default", "acceptEdits", "auto", "bypassPermissions"},
			NativeModeDoc:         "Native Claude permission mode a worker starts in; it never exceeds the lead's mode. plan sets the worker's level to plan: it starts in default, and the gate keeps it read-only. Absent means duolog's default.",
		},
	}
	if !reflect.DeepEqual(Facts, want) {
		t.Fatalf("Facts = %+v, want %+v", Facts, want)
	}
}

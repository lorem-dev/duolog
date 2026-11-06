package workerenv

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// A route name counts as dropped when a worker no longer gets it: set in
// duolog's own environment, even empty, or listed in env_passthrough, while
// paid_runs does not let it through. Listing a key is not enough any more,
// so the person learns why it did not reach the worker. Allowed paid runs
// with the key in their env end the line.
func TestDroppedKeysSetOrListedNotAllowed(t *testing.T) {
	claude := []string{"ANTHROPIC_API_KEY"}
	codex := []string{"OPENAI_API_KEY"}
	trusted := map[string]string{"corp-gateway": "https://llm.example.com/claude"}
	for _, c := range []struct {
		name           string
		names, environ []string
		paid           func(*config.PaidRuns)
		want           map[string][]string
	}{
		{"neither", []string{"PATH", "HOME"}, []string{"PATH=/bin", "HOME=/home"}, nil, map[string][]string{}},
		{"set", nil, []string{"ANTHROPIC_API_KEY=sk-test-value"}, nil, map[string][]string{"claude": claude}},
		{"set empty", nil, []string{"OPENAI_API_KEY="}, nil, map[string][]string{"codex": codex}},
		{"listed not set", []string{"PATH", "ANTHROPIC_API_KEY"}, []string{"PATH=/bin"}, nil, map[string][]string{"claude": claude}},
		{"listed and set", []string{"OPENAI_API_KEY"}, []string{"OPENAI_API_KEY=sk-test-value", "ANTHROPIC_API_KEY="}, nil,
			map[string][]string{"claude": claude, "codex": codex}},
		{"similar names", []string{"ANTHROPIC_API_KEY_OLD"}, []string{"XOPENAI_API_KEY=x", "ANTHROPIC_API_KEYS=x"}, nil, map[string][]string{}},
		{"similar listed name", []string{"ANTHROPIC_API_KEY_OLD"}, []string{"ANTHROPIC_API_KEY=x"}, nil, map[string][]string{"claude": claude}},
		{"other route shapes", []string{"CODEX_API_KEY"}, []string{"ANTHROPIC_BASE_URL=https://other.example.com", "CLAUDE_CODE_USE_BEDROCK=1"}, nil,
			map[string][]string{"claude": {"ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_BEDROCK"}, "codex": {"CODEX_API_KEY"}}},
		{"trusted base URL", []string{"ANTHROPIC_BASE_URL"}, []string{"ANTHROPIC_BASE_URL=https://llm.example.com/claude/"},
			func(p *config.PaidRuns) { p.Claude.SubscriptionEndpoints = trusted }, map[string][]string{}},
		{"allowed with the key in its env", nil, []string{"ANTHROPIC_API_KEY=x", "OPENAI_API_KEY=y"},
			func(p *config.PaidRuns) {
				p.Claude.Allow, p.Claude.DailyCapUSD, p.Claude.Env = true, 5, []string{"ANTHROPIC_API_KEY"}
			}, map[string][]string{"codex": codex}},
		{"allowed and listed", []string{"ANTHROPIC_AUTH_TOKEN"}, []string{"ANTHROPIC_AUTH_TOKEN=x"},
			func(p *config.PaidRuns) { p.Claude.Allow, p.Claude.DailyCapUSD = true, 5 }, map[string][]string{}},
		{"allowed without the key", nil, []string{"ANTHROPIC_API_KEY=x"},
			func(p *config.PaidRuns) { p.Claude.Allow, p.Claude.DailyCapUSD = true, 5 }, map[string][]string{"claude": claude}},
	} {
		cfg := &config.Config{EnvPassthrough: c.names}
		if c.paid != nil {
			c.paid(&cfg.PaidRuns)
		}
		if got := DroppedKeys(cfg, c.environ); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: DroppedKeys = %v, want %v", c.name, got, c.want)
		}
	}

	// The way back is the paid_runs opt-in, no longer env_passthrough. The
	// paid env adds to env_passthrough, so the line needs no whole list.
	if got, want := MigrationLine("claude", "ANTHROPIC_API_KEY"), "claude workers no longer get ANTHROPIC_API_KEY. To let them use it, set paid_runs.claude.allow: true with a daily_cap_usd above 0 and list the key in paid_runs.claude.env."; got != want {
		t.Errorf("MigrationLine = %q, want %q", got, want)
	}
	if got, want := MigrationLine("codex", "OPENAI_API_KEY"), "codex workers no longer get OPENAI_API_KEY. To let them use it, set paid_runs.codex.allow: true with a daily_cap_usd above 0 and list the key in paid_runs.codex.env."; got != want {
		t.Errorf("MigrationLine = %q, want %q", got, want)
	}
	for _, agent := range []string{"claude", "codex"} {
		if line := MigrationLine(agent, "KEY"); strings.Contains(line, "env_passthrough") {
			t.Errorf("MigrationLine still advises env_passthrough: %q", line)
		}
	}
}

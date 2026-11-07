package route

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func switchOffCodex(cfg *config.Config) {
	off := false
	cfg.Agents["codex"] = config.AgentSwitch{Enabled: &off}
}

// TestSwitchedOffAgentRefused: an agent whose agents.<name>.enabled is false
// is unavailable to a role, so the role moves to the next enabled agent,
// whether the switched-off agent is its use agent, its configured fallback or
// the derived fallback. Only a pinned use of it is refused.
func TestSwitchedOffAgentRefused(t *testing.T) {
	for _, tc := range []struct {
		name        string
		unavailable map[string]bool
		use, fall   config.Target
		wantAgent   string
	}{
		{"use agent", nil,
			config.Target{Agent: "codex", Model: "sol", Effort: "high"}, config.Target{Agent: "claude", Model: "sonnet", Effort: "high"}, "claude"},
		{"fallback target", map[string]bool{"claude": true},
			config.Target{Agent: "claude", Model: "sonnet", Effort: "high"}, config.Target{Agent: "codex", Model: "sol", Effort: "high"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults(t)
			r := cfg.Roles["implement_complex"]
			r.Use, r.Fallback = tc.use, tc.fall
			cfg.Roles["implement_complex"] = r
			switchOffCodex(cfg)
			got, err := Resolve(cfg, "implement_complex", tc.unavailable, Options{})
			if tc.wantAgent == "" {
				if err == nil || !strings.Contains(err.Error(), "no available agent") {
					t.Fatalf("route %+v, err %v; want no available agent", got, err)
				}
				return
			}
			if err != nil || got.Agent != tc.wantAgent || !got.IsFallback {
				t.Fatalf("route %+v, err %v; want fallback to %s", got, err, tc.wantAgent)
			}
		})
	}
}

// TestSwitchedOffUseMovesToFallback: with codex switched off, implement (use
// codex, fallback claude) runs on claude.
func TestSwitchedOffUseMovesToFallback(t *testing.T) {
	cfg := defaults(t)
	switchOffCodex(cfg)
	got, err := Resolve(cfg, "implement", nil, Options{})
	if err != nil || got.Agent != "claude" || !got.IsFallback {
		t.Fatalf("route %+v, err %v; want fallback to claude", got, err)
	}
}

// TestPinnedSwitchedOffAgentRefused: a pinned use (--model, a calibration
// entry) is not moved; it is refused.
func TestPinnedSwitchedOffAgentRefused(t *testing.T) {
	cfg := defaults(t)
	switchOffCodex(cfg)
	pin := config.Target{Agent: "codex", Model: "sol", Effort: "high"}
	got, err := Resolve(cfg, "implement", nil, Options{Use: &pin})
	want := "role implement: agent codex is switched off (agents.codex.enabled is false)"
	if err == nil || err.Error() != want {
		t.Fatalf("route %+v, err %v; want %q", got, err, want)
	}
}

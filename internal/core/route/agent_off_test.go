package route

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// TestSwitchedOffAgentRefused: an agent whose agents.<name>.enabled is false
// is refused as a role's use agent and as the fallback target the role sets,
// not only as a derived fallback.
func TestSwitchedOffAgentRefused(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name        string
		unavailable map[string]bool
		use, fall   config.Target
	}{
		{"use agent", nil,
			config.Target{Agent: "codex", Model: "sol", Effort: "high"}, config.Target{Agent: "claude", Model: "sonnet", Effort: "high"}},
		{"fallback target", map[string]bool{"claude": true},
			config.Target{Agent: "claude", Model: "sonnet", Effort: "high"}, config.Target{Agent: "codex", Model: "sol", Effort: "high"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults(t)
			r := cfg.Roles["implement_complex"]
			r.Use, r.Fallback = tc.use, tc.fall
			cfg.Roles["implement_complex"] = r
			cfg.Agents["codex"] = config.AgentSwitch{Enabled: &off}
			got, err := Resolve(cfg, "implement_complex", tc.unavailable, Options{})
			if err == nil || !strings.Contains(err.Error(), "agent codex is switched off") {
				t.Fatalf("route %+v, err %v; want codex switched off", got, err)
			}
		})
	}
}

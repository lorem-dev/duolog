package router

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
)

func TestResolveBalancesByPace(t *testing.T) {
	cfg := defaults(t)
	cfg.Roles["implement"] = config.Role{
		Use:      config.Target{Agent: "codex", Model: "gpt-5", Effort: "high"},
		Fallback: config.Target{Agent: "claude", Model: "sonnet", Effort: "high"},
		Sandbox:  "workspace-write", Context: "project", Session: "fresh",
	}
	ahead := Balance{Mode: "pace", Margin: .1, Pace: map[string]AgentPace{
		"codex":  {Subscription: true, Week: &PaceWindow{Used: .61, Elapsed: .44}},
		"claude": {Subscription: true, Week: &PaceWindow{Used: .30, Elapsed: .44}},
	}}
	route, err := Resolve(cfg, "implement", nil, Options{Balance: &ahead})
	if err != nil {
		t.Fatal(err)
	}
	if route.Agent != "claude" || route.Model != "sonnet" || !route.IsFallback ||
		route.Reason != "implement -> claude sonnet: codex 61% of the week at 44% elapsed" || !route.Ahead["codex"] {
		t.Errorf("balanced route = %+v", route)
	}

	// A pinned target, an unavailable fallback and no balance keep use.
	pinned := config.Target{Agent: "codex", Model: "gpt-5", Effort: "low"}
	for name, c := range map[string]struct {
		un   map[string]bool
		opts Options
	}{
		"pinned":               {nil, Options{Balance: &ahead, Use: &pinned}},
		"fallback unavailable": {map[string]bool{"claude": true}, Options{Balance: &ahead}},
		"no balance":           {nil, Options{}},
	} {
		got, err := Resolve(cfg, "implement", c.un, c.opts)
		if err != nil || got.Agent != "codex" || got.Reason != "" || got.IsFallback {
			t.Errorf("%s: route = %+v, %v; want codex with no reason", name, got, err)
		}
	}

	// An unavailable use agent keeps the existing fallback path, with no pace reason.
	got, err := Resolve(cfg, "implement", map[string]bool{"codex": true}, Options{Balance: &ahead})
	if err != nil || got.Agent != "claude" || got.Reason != "" {
		t.Errorf("unavailable use: route = %+v, %v", got, err)
	}
}

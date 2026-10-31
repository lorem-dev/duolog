package router

import (
	"github.com/lorem-dev/duolog/internal/config"
	"strings"
	"testing"
)

func defaults(t *testing.T) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func checkRoute(t *testing.T, role string, unavailable map[string]bool, lead *config.Target, change func(*config.Config), want config.Target, fallback, self, inLead, capped bool) {
	t.Helper()
	cfg := defaults(t)
	if change != nil {
		change(cfg)
	}
	got, err := Resolve(cfg, role, unavailable, Options{Lead: lead})
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Roles[role]
	if got.Target != want || got.IsFallback != fallback || got.Self != self || got.InLead != inLead || got.EffortCapped != capped || got.Sandbox != r.Sandbox || got.Context != r.Context || got.Session != r.Session {
		t.Fatalf("unexpected route: %+v; want target %+v, fallback=%v self=%v in_lead=%v capped=%v", got, want, fallback, self, inLead, capped)
	}
}
func TestPrimaryRoute(t *testing.T) {
	checkRoute(t, "review_intermediate", nil, nil, nil, config.Target{Agent: "codex", Model: "terra", Effort: "medium"}, false, false, false, false)
}
func TestFallbackWhenUnavailable(t *testing.T) {
	checkRoute(t, "review_intermediate", map[string]bool{"codex": true}, nil, nil, config.Target{Agent: "claude", Model: "sonnet", Effort: "medium"}, true, false, false, false)
}
func TestFallbackGoesToLeadAgent(t *testing.T) {
	checkRoute(t, "review_intermediate", map[string]bool{"codex": true}, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, nil, config.Target{Agent: "claude", Model: "sonnet", Effort: "medium"}, true, false, true, false)
}
func TestFallbackToOtherAgentIsReplaced(t *testing.T) {
	checkRoute(t, "review_intermediate", map[string]bool{"codex": true}, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, func(cfg *config.Config) {
		r := cfg.Roles["review_intermediate"]
		r.Fallback = config.Target{Agent: "codex", Model: "sol", Effort: "high"}
		cfg.Roles["review_intermediate"] = r
	}, config.Target{Agent: "claude", Model: "sonnet", Effort: "medium"}, true, false, true, false)
}
func TestMissingFallbackDerivedFromMap(t *testing.T) {
	checkRoute(t, "implement_complex", map[string]bool{"codex": true}, nil, func(cfg *config.Config) {
		r := cfg.Roles["implement_complex"]
		r.Fallback = config.Target{}
		cfg.Roles["implement_complex"] = r
	}, config.Target{Agent: "claude", Model: "opus", Effort: "high"}, true, false, false, false)
}
func TestCheapRoleStaysCheap(t *testing.T) {
	checkRoute(t, "mechanical", map[string]bool{"codex": true}, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, nil, config.Target{Agent: "claude", Model: "haiku", Effort: "low"}, true, false, true, false)
}
func TestFallbackCappedByLead(t *testing.T) {
	checkRoute(t, "implement_complex", map[string]bool{"codex": true}, &config.Target{Agent: "claude", Model: "opus", Effort: "low"}, nil, config.Target{Agent: "claude", Model: "opus", Effort: "low"}, true, false, true, true)
}
func TestLimitFallbackRunsInLead(t *testing.T) {
	checkRoute(t, "tests", map[string]bool{"codex": true}, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, nil, config.Target{Agent: "claude", Model: "sonnet", Effort: "medium"}, true, false, true, false)
}
func TestPrimaryRouteIsNotInLead(t *testing.T) {
	checkRoute(t, "plan", nil, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, nil, config.Target{Agent: "claude", Model: "opus", Effort: "high"}, false, true, false, false)
}
func TestFallbackWorksWhenCodexLeads(t *testing.T) {
	checkRoute(t, "plan", map[string]bool{"claude": true}, &config.Target{Agent: "codex", Model: "sol", Effort: "high"}, nil, config.Target{Agent: "codex", Model: "sol", Effort: "high"}, true, true, true, false)
}
func TestSelfWhenLeadMatchesResumeRole(t *testing.T) {
	checkRoute(t, "plan", nil, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, nil, config.Target{Agent: "claude", Model: "opus", Effort: "high"}, false, true, false, false)
}
func TestSelfNeedsSameModel(t *testing.T) {
	checkRoute(t, "plan", nil, &config.Target{Agent: "claude", Model: "sonnet", Effort: "high"}, nil, config.Target{Agent: "claude", Model: "opus", Effort: "high"}, false, false, false, false)
}
func TestCheapLeadDoesMinimalRole(t *testing.T) {
	checkRoute(t, "mechanical", nil, &config.Target{Agent: "claude", Model: "haiku", Effort: "high"}, nil, config.Target{Agent: "codex", Model: "luna", Effort: "low"}, false, true, false, false)
}
func TestFreshRoleNeverSelf(t *testing.T) {
	checkRoute(t, "review_final", nil, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, nil, config.Target{Agent: "claude", Model: "opus", Effort: "high"}, false, false, false, false)
}
func TestCodexLeadStillRoutesPlanToClaude(t *testing.T) {
	checkRoute(t, "plan", nil, &config.Target{Agent: "codex", Model: "sol", Effort: "high"}, nil, config.Target{Agent: "claude", Model: "opus", Effort: "high"}, false, false, false, false)
}
func TestNoLeadMeansNoSelf(t *testing.T) {
	checkRoute(t, "plan", nil, nil, nil, config.Target{Agent: "claude", Model: "opus", Effort: "high"}, false, false, false, false)
}
func TestEffortCappedByLead(t *testing.T) {
	checkRoute(t, "implement_complex", nil, &config.Target{Agent: "claude", Model: "opus", Effort: "medium"}, nil, config.Target{Agent: "codex", Model: "sol", Effort: "medium"}, false, false, false, true)
}
func TestLeadHighDoesNotLowerHigh(t *testing.T) {
	checkRoute(t, "implement_complex", nil, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, nil, config.Target{Agent: "codex", Model: "sol", Effort: "high"}, false, false, false, false)
}
func TestLeadEffortNeverRaises(t *testing.T) {
	checkRoute(t, "mechanical", nil, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, nil, config.Target{Agent: "codex", Model: "luna", Effort: "low"}, false, false, false, false)
}
func TestCapAppliesToFallback(t *testing.T) {
	checkRoute(t, "implement_complex", map[string]bool{"codex": true}, &config.Target{Agent: "codex", Model: "sol", Effort: "low"}, nil, config.Target{Agent: "claude", Model: "opus", Effort: "low"}, true, false, false, true)
}
func TestCapWorksWhenCodexLeads(t *testing.T) {
	checkRoute(t, "plan", nil, &config.Target{Agent: "codex", Model: "sol", Effort: "medium"}, nil, config.Target{Agent: "claude", Model: "opus", Effort: "medium"}, false, false, false, true)
}
func TestUnknownLeadEffortDoesNotCap(t *testing.T) {
	checkRoute(t, "implement_complex", nil, &config.Target{Agent: "claude", Model: "opus", Effort: ""}, nil, config.Target{Agent: "codex", Model: "sol", Effort: "high"}, false, false, false, false)
}
func TestUltraAllowedUnderUltraLead(t *testing.T) {
	checkRoute(t, "implement_complex", nil, &config.Target{Agent: "claude", Model: "opus", Effort: "ultra"}, func(cfg *config.Config) {
		r := cfg.Roles["implement_complex"]
		r.Use.Effort = "ultra"
		cfg.Roles["implement_complex"] = r
	}, config.Target{Agent: "codex", Model: "sol", Effort: "ultra"}, false, false, false, false)
}
func TestUltraCappedByHighLead(t *testing.T) {
	checkRoute(t, "implement_complex", nil, &config.Target{Agent: "claude", Model: "opus", Effort: "high"}, func(cfg *config.Config) {
		r := cfg.Roles["implement_complex"]
		r.Use.Effort = "ultra"
		cfg.Roles["implement_complex"] = r
	}, config.Target{Agent: "codex", Model: "sol", Effort: "high"}, false, false, false, true)
}
func TestUltraWithUnknownLead(t *testing.T) {
	checkRoute(t, "implement_complex", nil, &config.Target{Agent: "claude", Model: "opus", Effort: ""}, func(cfg *config.Config) {
		r := cfg.Roles["implement_complex"]
		r.Use.Effort = "ultra"
		cfg.Roles["implement_complex"] = r
	}, config.Target{Agent: "codex", Model: "sol", Effort: "ultra"}, false, false, false, false)
}
func TestConcreteLeadMatchesAlias(t *testing.T) {
	checkRoute(t, "plan", nil, &config.Target{Agent: "claude", Model: "claude-opus-5-5", Effort: "high"}, nil, config.Target{Agent: "claude", Model: "opus", Effort: "high"}, false, true, false, false)
}
func TestExpensiveLeadDelegatesMinimalRole(t *testing.T) {
	checkRoute(t, "mechanical", nil, &config.Target{Agent: "claude", Model: "sonnet", Effort: "high"}, nil, config.Target{Agent: "codex", Model: "luna", Effort: "low"}, false, false, false, false)
}

func TestBothUnavailableIsError(t *testing.T) {
	_, err := Resolve(defaults(t), "plan", map[string]bool{"claude": true, "codex": true}, Options{})
	if err == nil || !strings.Contains(err.Error(), "plan") {
		t.Fatalf("got %v", err)
	}
}
func TestUnknownRole(t *testing.T) {
	_, err := Resolve(defaults(t), "missing", nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("got %v", err)
	}
}
func TestManualModelRefused(t *testing.T) {
	for _, model := range []string{"fable", "claude-fable-5-1", "astra", "gpt-6-astra"} {
		cfg := defaults(t)
		r := cfg.Roles["plan"]
		r.Use.Model = model
		if strings.Contains(model, "astra") {
			r.Use.Agent = "codex"
		}
		cfg.Roles["plan"] = r
		if _, err := Resolve(cfg, "plan", nil, Options{}); err == nil {
			t.Fatalf("accepted %s", model)
		}
		if _, err := Resolve(cfg, "plan", nil, Options{AllowManualModel: true}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestEffortScale(t *testing.T) {
	levels := []string{"low", "medium", "high", "xhigh", "max", "ultra"}
	for i, roleEffort := range levels {
		for j, leadEffort := range levels {
			want := roleEffort
			if j < i {
				want = leadEffort
			}
			checkRoute(t, "implement_complex", nil, &config.Target{Agent: "claude", Model: "opus", Effort: leadEffort}, func(cfg *config.Config) {
				r := cfg.Roles["implement_complex"]
				r.Use.Effort = roleEffort
				cfg.Roles["implement_complex"] = r
			}, config.Target{Agent: "codex", Model: "sol", Effort: want}, false, false, false, j < i)
		}
	}
}

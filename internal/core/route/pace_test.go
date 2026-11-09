package route

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func win(used, elapsed float64) *PaceWindow { return &PaceWindow{Used: used, Elapsed: elapsed} }

func TestElapsedAtWindowEdges(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	for _, tc := range []struct {
		name   string
		resets time.Time
		want   float64
		ok     bool
	}{
		{"just reset", now.Add(week), 0, true},
		{"half", now.Add(week / 2), .5, true},
		{"one second left", now.Add(time.Second), 1 - 1/week.Seconds(), true},
		{"reset is now", now, 0, false},
		{"reset passed: the read is stale", now.Add(-time.Minute), 0, false},
		{"no reset time", time.Time{}, 0, false},
		{"reset beyond the window: clock skew counts as just reset", now.Add(week + time.Hour), 0, true},
	} {
		got, ok := Elapsed(tc.resets, week, now)
		if ok != tc.ok || math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: Elapsed = %v, %v; want %v, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := Elapsed(now.Add(time.Hour), 0, now); ok {
		t.Error("a window without a length has no elapsed share")
	}
}

func TestAheadOfPace(t *testing.T) {
	const margin = .10
	for _, tc := range []struct {
		name string
		a    AgentPace
		was  bool
		want bool
	}{
		{"on pace", AgentPace{Week: win(.40, .44)}, false, false},
		{"exactly the margin ahead is not ahead", AgentPace{Week: win(.54, .44)}, false, false},
		{"past the margin", AgentPace{Week: win(.55, .44)}, false, true},
		{"fresh window, small use", AgentPace{Week: win(.05, 0)}, false, false},
		{"fresh window, use past the margin", AgentPace{Week: win(.11, 0)}, false, true},
		{"end of the window, all used", AgentPace{Week: win(1, 1)}, false, false},
		{"five hours above 0.80 alone", AgentPace{Week: win(.10, .50), FiveHour: win(.81, .5)}, false, true},
		{"five hours at 0.80", AgentPace{Week: win(.10, .50), FiveHour: win(.80, .5)}, false, false},
		{"unknown: no weekly window", AgentPace{FiveHour: win(.95, .5)}, false, false},
		// Hysteresis: once balanced away, stay until below pace minus margin.
		{"was ahead, back on pace but not below", AgentPace{Week: win(.44, .44)}, true, true},
		{"was ahead, at pace minus margin", AgentPace{Week: win(.34, .44)}, true, true},
		{"was ahead, below pace minus margin", AgentPace{Week: win(.33, .44)}, true, false},
		{"was ahead, five hours still above 0.80", AgentPace{Week: win(.10, .50), FiveHour: win(.85, .5)}, true, true},
	} {
		if got := Ahead(tc.a, margin, tc.was); got != tc.want {
			t.Errorf("%s: Ahead = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPaceWeekReasonText(t *testing.T) {
	got := AgentPace{Week: win(.61, .44)}.Why("codex")
	if got != "codex 61% of the week at 44% elapsed" {
		t.Errorf("week reason = %q", got)
	}
	got = AgentPace{Week: win(.10, .50), FiveHour: win(.85, .5)}.Why("claude")
	if got != "claude 85% of the five hours" {
		t.Errorf("five-hour reason = %q", got)
	}
}

func role() config.Role {
	return config.Role{
		Use:      config.Target{Agent: "codex", Model: "gpt-5", Effort: "high"},
		Fallback: config.Target{Agent: "claude", Model: "sonnet", Effort: "high"},
	}
}

func sub(w *PaceWindow) AgentPace { return AgentPace{Subscription: true, Week: w} }

func TestBalanceMovesAheadUseToFallback(t *testing.T) {
	b := Balance{Mode: "pace", Margin: .10, Pace: map[string]AgentPace{
		"codex": sub(win(.61, .44)), "claude": sub(win(.30, .44)),
	}}
	d := b.Decide("implement", role())
	if !d.Move || d.Reason != "implement -> claude sonnet: codex 61% of the week at 44% elapsed" {
		t.Errorf("decision = %+v", d)
	}
	if !d.Ahead["codex"] || d.Ahead["claude"] {
		t.Errorf("Ahead = %v, want only codex", d.Ahead)
	}
}

func TestBalanceUseStays(t *testing.T) {
	r := role()
	for _, tc := range []struct {
		name string
		b    Balance
	}{
		{"off", Balance{Mode: "off", Margin: .1, Pace: map[string]AgentPace{"codex": sub(win(.9, .1)), "claude": sub(win(.1, .5))}}},
		{"use on pace", Balance{Mode: "pace", Margin: .1, Pace: map[string]AgentPace{"codex": sub(win(.4, .44)), "claude": sub(win(.1, .5))}}},
		{"unknown quota for use", Balance{Mode: "pace", Margin: .1, Pace: map[string]AgentPace{"claude": sub(win(.1, .5))}}},
		{"unknown quota for fallback", Balance{Mode: "pace", Margin: .1, Pace: map[string]AgentPace{"codex": sub(win(.9, .1))}}},
		{"no quota at all", Balance{Mode: "pace", Margin: .1}},
		{"use not on subscription", Balance{Mode: "pace", Margin: .1, Pace: map[string]AgentPace{
			"codex": {Week: win(.9, .1)}, "claude": sub(win(.1, .5))}}},
		{"fallback ahead of pace too", Balance{Mode: "pace", Margin: .1, Pace: map[string]AgentPace{
			"codex": sub(win(.9, .1)), "claude": sub(win(.7, .2))}}},
		{"fallback paid", Balance{Mode: "pace", Margin: .1, Pace: map[string]AgentPace{
			"codex": sub(win(.9, .1)), "claude": {Week: win(.1, .5)}}}},
		{"fallback is the same agent", Balance{Mode: "pace", Margin: .1, Pace: map[string]AgentPace{"codex": sub(win(.9, .1))}}},
	} {
		role := r
		if tc.name == "fallback is the same agent" {
			role.Fallback.Agent = "codex"
		}
		if d := tc.b.Decide("implement", role); d.Move || d.Reason != "" {
			t.Errorf("%s: decision = %+v, want use to stay", tc.name, d)
		}
	}
}

func TestBalanceHysteresis(t *testing.T) {
	r := role()
	mk := func(used float64, was bool) Decision {
		b := Balance{Mode: "pace", Margin: .10, Was: map[string]bool{"codex": was}, Pace: map[string]AgentPace{
			"codex": sub(win(used, .44)), "claude": sub(win(.2, .44))}}
		return b.Decide("implement", r)
	}
	// Not balanced yet: 0.50 is on pace plus margin, so use stays.
	if d := mk(.50, false); d.Move {
		t.Errorf("0.50 moved without hysteresis state: %+v", d)
	}
	// Balanced away: it stays away until below pace minus margin (0.34).
	if d := mk(.50, true); !d.Move {
		t.Errorf("0.50 came back too early: %+v", d)
	}
	if d := mk(.36, true); !d.Move {
		t.Errorf("0.36 came back too early: %+v", d)
	}
	if d := mk(.33, true); d.Move || d.Ahead["codex"] {
		t.Errorf("0.33 did not come back: %+v", d)
	}
}

func TestBalanceJudgesFallbackWithoutHysteresis(t *testing.T) {
	// A fallback that was balanced away from earlier uses the stateless
	// test when it is the target: ahead of pace is judged without hysteresis.
	b := Balance{Mode: "pace", Margin: .10, Was: map[string]bool{"claude": true}, Pace: map[string]AgentPace{
		"codex": sub(win(.9, .1)), "claude": sub(win(.45, .44))}}
	d := b.Decide("implement", role())
	if !d.Move || !strings.Contains(d.Reason, "claude sonnet") {
		t.Errorf("decision = %+v; want a move to claude", d)
	}
}

func TestBalanceFalseKeepsRoleOnUse(t *testing.T) {
	b := Balance{Mode: "pace", Margin: .10, Pace: map[string]AgentPace{
		"codex": sub(win(.9, .1)), "claude": sub(win(.1, .5)),
	}}
	off, on := false, true
	r := role()
	r.Balance = &off
	if d := b.Decide("planner", r); d.Move || d.Reason != "" {
		t.Errorf("balance false: decision = %+v, want use to stay", d)
	}
	r.Balance = &on
	if d := b.Decide("planner", r); !d.Move {
		t.Errorf("balance true: decision = %+v, want a move", d)
	}
	if d := b.Decide("planner", role()); !d.Move {
		t.Errorf("balance unset: decision = %+v, want a move", d)
	}
}

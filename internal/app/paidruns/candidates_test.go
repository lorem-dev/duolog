package paidruns

import (
	"context"
	"reflect"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

var (
	claudeSonnet = config.Target{Agent: "claude", Model: "sonnet", Effort: "medium"}
	codexSol     = config.Target{Agent: "codex", Model: "sol", Effort: "medium"}
)

func free(t config.Target) paidrules.Candidate {
	return paidrules.Candidate{Target: t, Variant: workerenv.Free}
}
func paid(t config.Target) paidrules.Candidate {
	return paidrules.Candidate{Target: t, Variant: workerenv.Paid}
}

// optIn allows paid runs of the agents with a daily cap.
func optIn(agents ...string) config.PaidRuns {
	var p config.PaidRuns
	for _, agent := range agents {
		a := p.Agent(agent)
		a.Allow, a.DailyCapUSD = true, 5
		p.Set(agent, a)
	}
	return p
}

// Every free candidate of the role, use then fallback, comes before any paid
// variant, and only the agent's own opt-in creates its paid variant. A
// target the person pinned keeps its agent: its paid variant comes before
// another agent. The lead is never a candidate: it is where the task goes
// when none is left, and the gate never builds or runs it. A balance score
// uses the usable headroom once, and a paid route has no window score that
// could put it ahead of a usable subscription.
func TestCandidatesFreeBeforePaidAndPinnedAgent(t *testing.T) {
	role := []config.Target{claudeSonnet, codexSol}
	cases := []struct {
		name   string
		pinned bool
		paid   config.PaidRuns
		want   []paidrules.Candidate
	}{
		{"no opt-in", false, config.PaidRuns{}, []paidrules.Candidate{free(claudeSonnet), free(codexSol)}},
		{"use opted in", false, optIn("claude"), []paidrules.Candidate{free(claudeSonnet), free(codexSol), paid(claudeSonnet)}},
		{"fallback opted in", false, optIn("codex"), []paidrules.Candidate{free(claudeSonnet), free(codexSol), paid(codexSol)}},
		{"both opted in", false, optIn("claude", "codex"),
			[]paidrules.Candidate{free(claudeSonnet), free(codexSol), paid(claudeSonnet), paid(codexSol)}},
		{"pinned keeps its agent", true, optIn("claude", "codex"),
			[]paidrules.Candidate{free(claudeSonnet), paid(claudeSonnet), free(codexSol), paid(codexSol)}},
		{"pinned without opt-in", true, optIn("codex"),
			[]paidrules.Candidate{free(claudeSonnet), free(codexSol), paid(codexSol)}},
	}
	for _, c := range cases {
		if got := paidrules.Candidates(role, c.pinned, c.paid); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v\nwant %+v", c.name, got, c.want)
		}
	}

	// allow without a cap, or a cap without allow, is no opt-in.
	var halves config.PaidRuns
	halves.Set("claude", config.PaidAgent{Allow: true})
	halves.Set("codex", config.PaidAgent{DailyCapUSD: 5})
	if got := paidrules.Candidates(role, false, halves); !reflect.DeepEqual(got, []paidrules.Candidate{free(claudeSonnet), free(codexSol)}) {
		t.Errorf("half an opt-in made a paid variant: %+v", got)
	}

	// No target, an empty fallback and a repeated target add nothing.
	if got := paidrules.Candidates(nil, true, optIn("claude")); len(got) != 0 {
		t.Errorf("no targets: %+v", got)
	}
	got := paidrules.Candidates([]config.Target{claudeSonnet, {}, claudeSonnet}, false, optIn("claude"))
	if want := []paidrules.Candidate{free(claudeSonnet), paid(claudeSonnet)}; !reflect.DeepEqual(got, want) {
		t.Errorf("repeated targets: %+v, want %+v", got, want)
	}
	for _, c := range paidrules.Candidates(role, true, optIn("claude", "codex")) {
		if c.Target.Agent != "claude" && c.Target.Agent != "codex" {
			t.Errorf("a candidate that is not a worker agent: %+v", c)
		}
	}

	// Lead placement: the gate refuses the lead shape before it builds,
	// reads or reserves anything.
	w := newGateWorld(t)
	req := w.request("claude", workerenv.Lead, workerenv.Free, 100)
	if _, err := w.gate.Acquire(context.Background(), req); err == nil {
		t.Fatal("the gate admitted the lead")
	}
	if w.builds != 0 || w.reads != 0 || len(w.state().Reservations) != 0 {
		t.Fatalf("lead placement built %d, read %d, reserved %+v", w.builds, w.reads, w.state().Reservations)
	}

	// Usable headroom: the tightest window's 1 - reserve - (used + reserved
	// + cost)/u, with u applied once.
	costs := []paidrules.WindowCost{
		{Key: paidrules.WindowKey{Kind: "session"}, Used: 0.2, Reserved: 0.05, Cost: 0.05, Reserve: 0.10},
		{Key: paidrules.WindowKey{Kind: "weekly_all"}, Used: 0.1, Cost: 0.01, Reserve: 0.15},
	}
	for _, c := range []struct{ u, want float64 }{{1, 0.6}, {0.5, 0.3}} {
		h, ok := paidrules.Headroom(costs, c.u)
		if !ok || !near(h, c.want) {
			t.Errorf("headroom at u=%v: %v %v, want %v", c.u, h, ok, c.want)
		}
	}
	// A full window leaves no headroom, never a negative score that could
	// still rank; a paid route has no windows and so no score at all.
	if h, ok := paidrules.Headroom([]paidrules.WindowCost{{Used: 0.95, Cost: 0.05, Reserve: 0.10}}, 1); !ok || h != 0 {
		t.Errorf("full window: %v %v", h, ok)
	}
	for _, bad := range []struct {
		costs []paidrules.WindowCost
		u     float64
	}{{nil, 1}, {costs, 0}, {costs, 1.5}, {[]paidrules.WindowCost{{Used: -1}}, 1}} {
		if h, ok := paidrules.Headroom(bad.costs, bad.u); ok || h != 0 {
			t.Errorf("Headroom(%+v, %v) =%v %v, want no score", bad.costs, bad.u, h, ok)
		}
	}
	// The gate gives a paid permit no windows: no invented rate.
	pa := w.cfg.PaidRuns.Agent("claude")
	pa.Allow, pa.DailyCapUSD = true, 50
	pa.Env = []string{"ANTHROPIC_API_KEY"}
	w.cfg.PaidRuns.Set("claude", pa)
	permit := w.acquire(w.request("claude", workerenv.Worker, workerenv.Paid, 101))
	if permit.Assessment.Verdict != paidrules.Paid || len(permit.Reservation.Estimate.Windows) != 0 {
		t.Fatalf("paid permit: %+v", permit)
	}
	if _, ok := paidrules.Headroom(permit.Reservation.Estimate.Windows, permit.Assessment.UsableShare); ok {
		t.Fatal("a paid permit has a headroom score")
	}
}

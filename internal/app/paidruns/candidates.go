package paidruns

import (
	"slices"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Candidate is one way to run a task: a target and the variant of its
// agent's process.
type Candidate struct {
	Target  config.Target
	Variant workerenv.Variant
}

// Candidates orders the ways to run a task of a role whose targets are
// targets, use first, then fallback. Every free variant comes before any
// paid one, so a paid run never takes a task that a subscription could
// serve. A paid variant exists only for an agent with paid runs allowed and
// a daily cap above 0. When pinned, the first target is the person's choice
// (--use or a profile): it keeps its agent, so its paid variant comes before
// any other agent.
//
// The lead is never a candidate. When none is admitted, the task goes to
// the lead with the refusals' reasons, and nothing is built or run for it.
// Targets without an agent and repeats add nothing.
func Candidates(targets []config.Target, pinned bool,
	paid config.PaidRuns) []Candidate {
	var out []Candidate
	add := func(t config.Target, v workerenv.Variant) {
		if t.Agent == "" || v == workerenv.Paid && !optedIn(paid, t.Agent) {
			return
		}
		if c := (Candidate{Target: t, Variant: v}); !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	rest := targets
	if pinned && len(targets) > 0 {
		add(targets[0], workerenv.Free)
		add(targets[0], workerenv.Paid)
		rest = targets[1:]
	}
	for _, v := range []workerenv.Variant{workerenv.Free, workerenv.Paid} {
		for _, t := range rest {
			add(t, v)
		}
	}
	return out
}

// optedIn reports whether agent allows paid runs with a cap to spend.
func optedIn(paid config.PaidRuns, agent string) bool {
	a := paid.Agent(agent)
	return a.Allow && a.DailyCapUSD > 0
}

// Headroom is the share of a window's usable part that a task would leave
// free in the tightest of its windows: 1 - reserve - (used + reserved +
// cost)/u, never below 0. The usable share divides once. A balance score
// uses it; a candidate without windows, such as a paid route, has no score,
// so no window rate is invented for it. An invalid share or value has none.
func Headroom(costs []WindowCost, usableShare float64) (float64, bool) {
	u := usableShare
	if len(costs) == 0 || !finite(u) || u <= 0 || u > 1 {
		return 0, false
	}
	left := 1.0
	for _, c := range costs {
		if !finite(c.Used, c.Reserved, c.Cost, c.Reserve) || min(c.Used, c.Reserved, c.Cost, c.Reserve) < 0 {
			return 0, false
		}
		left = min(left, 1-c.Reserve-(c.Used+c.Reserved+c.Cost)/u)
	}
	return max(left, 0), true
}

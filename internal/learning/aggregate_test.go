package learning

import (
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"testing"
)

func live() journal.Delegation {
	return journal.Delegation{ID: "d1", Task: "t1", Class: "debug", Ladder: "debug", Entry: "codex/terra/medium", Done: true, Priced: true, USD: 0.2, Seconds: 10}
}
func accepted() []journal.Task {
	return []journal.Task{{ID: "t1", Kind: "finish", Outcome: "accepted"}}
}
func TestAggregateOutcomeHalfWeight(t *testing.T) {
	d := live()
	d.Why = "person"
	s := Aggregate(nil, []journal.Delegation{d}, accepted(), nil)["debug"][d.Entry]
	if s.Weight != 0.5 || s.Quality != 3 || s.Delegations != 1 || s.USD != 0.2 || s.Seconds != 10 {
		t.Fatal(s)
	}
}
func TestAggregateEscalationCountsAsFailure(t *testing.T) {
	d := live()
	e := live()
	e.ID = "d2"
	e.Entry = "codex/sol/medium"
	e.EscalatedFrom = d.ID
	st := Aggregate(nil, []journal.Delegation{d, e}, accepted(), nil)["debug"]
	if s := st[d.Entry]; s.Failures != 1 || s.Quality != 0 || s.Weight != 0.5 {
		t.Fatal(s)
	}
	if s := st[e.Entry]; s.Quality != 3 || s.Weight != 0.5 {
		t.Fatal(s)
	}
}
func decidedScores(scores, correction []journal.Score, decision string) []journal.Verdict {
	return []journal.Verdict{{Kind: "verdict", ID: "v1", Ladder: "debug", Original: "codex/terra/medium", Profile: "claude/sonnet/high", Scores: scores}, {Kind: "decision", ID: "v1", Decision: decision, Scores: correction}}
}
func TestAggregateUndecidedVerdictIgnored(t *testing.T) {
	vs := decidedScores([]journal.Score{{Criterion: "a", Original: 3, Profile: 1}}, nil, "confirm")
	if st := Aggregate(nil, nil, nil, vs[:1]); len(st) != 0 {
		t.Fatal(st)
	}
}
func TestAggregateCorrectionOverridesJudge(t *testing.T) {
	vs := decidedScores([]journal.Score{{Criterion: "a", Original: 3, Profile: 1}}, []journal.Score{{Criterion: "a", Original: 1, Profile: 3}}, "correct")
	st := Aggregate(nil, nil, nil, vs)["debug"]
	if st[vs[0].Original].Quality != 1 || st[vs[0].Profile].Quality != 3 {
		t.Fatal(st)
	}
}
func TestAggregatePartialCorrectionKeepsJudgeScores(t *testing.T) {
	vs := decidedScores([]journal.Score{{Criterion: "a", Original: 3, Profile: 1}, {Criterion: "b", Original: 2, Profile: 2}}, []journal.Score{{Criterion: "a", Original: 1, Profile: 3}}, "correct")
	st := Aggregate(nil, nil, nil, vs)["debug"]
	if st[vs[0].Original].Quality != 1.5 || st[vs[0].Profile].Quality != 2.5 {
		t.Fatal(st)
	}
}
func TestAggregateSkipsInfra(t *testing.T) {
	for _, out := range []string{"infra", "lead"} {
		d := live()
		d.Outcome = out
		d.Done = false
		e := live()
		e.ID = "d2"
		e.Entry = "codex/sol/medium"
		e.EscalatedFrom = d.ID
		st := Aggregate(nil, []journal.Delegation{d, e}, accepted(), nil)["debug"]
		if st[d.Entry] != (config.EntryStat{}) || st[e.Entry].Failures != 0 {
			t.Fatal(st)
		}
	}
}
func TestAggregateProfileOnlyCostsAndPricedMeans(t *testing.T) {
	d := live()
	p := d
	p.ProfileOf = d.ID
	p.USD = 0.6
	p.Seconds = 30
	u := d
	u.Priced = false
	u.USD = 99
	u.Seconds = 20
	s := Aggregate(nil, []journal.Delegation{d, p, u}, accepted(), nil)["debug"][d.Entry]
	if s.Delegations != 2 || s.Weight != 1 || s.USD != 0.4 || s.Seconds != 20 {
		t.Fatal(s)
	}
}
func TestAggregateLatestDecisionAndOutcome(t *testing.T) {
	vs := decidedScores([]journal.Score{{Criterion: "a", Original: 3, Profile: 1}}, nil, "confirm")
	vs = append(vs, journal.Verdict{Kind: "decision", ID: "v1", Decision: "decline"})
	if st := Aggregate(nil, nil, nil, vs); len(st) != 0 {
		t.Fatal(st)
	}
	ts := append(accepted(), journal.Task{Kind: "outcome", ID: "t1", Outcome: "reworked"})
	if s := Aggregate(nil, []journal.Delegation{live()}, ts, nil)["debug"][live().Entry]; s.Quality != 1 {
		t.Fatal(s)
	}
}

func TestAggregateEscalationMustBeLater(t *testing.T) {
	d := live()
	e := live()
	e.ID = "d2"
	e.Entry = "codex/sol/medium"
	e.EscalatedFrom = d.ID
	s := Aggregate(nil, []journal.Delegation{e, d}, accepted(), nil)["debug"][d.Entry]
	if s.Failures != 0 || s.Quality != 3 {
		t.Fatal(s)
	}
}
func TestAggregateSkipsUnclassifiedAndTaskless(t *testing.T) {
	for _, kind := range []string{"unclassified", "taskless"} {
		d := live()
		if kind == "unclassified" {
			d.Class = Unclassified
		} else {
			d.Task = ""
		}
		if st := Aggregate(nil, []journal.Delegation{d}, nil, nil); len(st) != 0 {
			t.Fatal(st)
		}
	}
}

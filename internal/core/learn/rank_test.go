package learn

import (
	"slices"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/record"
)

func entryKeys(es []config.LadderEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Key())
	}
	return out
}
func TestRankPriorsKeepConfiguredOrder(t *testing.T) {
	l := defaults(t)
	for name, c := range l.Classes {
		for step, es := range c.Steps {
			if got := Rank(l, c, es, nil); !slices.Equal(got, entryKeys(es)) {
				t.Errorf("%s.%s: %v", name, step, got)
			}
		}
		if got := Rank(l, c, c.Ladder, nil); !slices.Equal(got, entryKeys(c.Ladder)) {
			t.Errorf("%s: %v", name, got)
		}
	}
}
func TestRankCostClassPicksCheapestQualifying(t *testing.T) {
	l := defaults(t)
	c := l.Classes["debug"]
	st := map[string]config.EntryStat{c.Ladder[0].Key(): {Weight: 3, Quality: 1}, c.Ladder[1].Key(): {Weight: 3, Quality: 3}}
	if got := Rank(l, c, c.Ladder, st); got[0] != c.Ladder[1].Key() {
		t.Fatal(got)
	}
}
func TestRankCostClassFallsBackToBestQuality(t *testing.T) {
	l := defaults(t)
	c := l.Classes["tests"]
	st := map[string]config.EntryStat{c.Ladder[0].Key(): {Weight: 4, Quality: 1.5}, c.Ladder[1].Key(): {Weight: 4, Quality: 2}}
	if got := Rank(l, c, c.Ladder, st); got[0] != c.Ladder[1].Key() {
		t.Fatal(got)
	}
}
func qualityRank(t *testing.T, q float64) []string {
	t.Helper()
	l := defaults(t)
	c := l.Classes["plan_review"]
	return Rank(l, c, c.Ladder, map[string]config.EntryStat{c.Ladder[0].Key(): {Delegations: 6, Weight: 6, Quality: 2.8, USD: 2}, c.Ladder[1].Key(): {Delegations: 6, Weight: 6, Quality: q, USD: 0.5}})
}
func TestRankQualityClassPrefersCheaperWithinMargin(t *testing.T) {
	if got := qualityRank(t, 2.7); got[0] != "codex/sol/high" {
		t.Fatal(got)
	}
}
func TestRankQualityClassKeepsBest(t *testing.T) {
	if got := qualityRank(t, 2.2); got[0] != "claude/opus/high" {
		t.Fatal(got)
	}
}
func TestEstimate(t *testing.T) {
	q, m := Estimate(config.Learning{PriorRuns: 2, Uncertainty: 0.5}, 3, config.EntryStat{Weight: 2, Quality: 1})
	if q != 2 || m != 0.25 {
		t.Fatal(q, m)
	}
}
func TestChangedIgnoresStatsOnlyChange(t *testing.T) {
	l := defaults(t)
	cfg := &config.Config{Learning: l}
	p := config.LearnedRouting{Ladders: map[string]config.LearnedLadder{"debug": {Order: entryKeys(l.Classes["debug"].Ladder), Stats: map[string]config.EntryStat{"codex/terra/medium": {Weight: 5}}}}}
	if Changed(cfg, p) {
		t.Fatal("stats changed order")
	}
	l.Learned = p.Ladders
	cfg.Learning = l
	if Changed(cfg, p) {
		t.Fatal("same learned order")
	}
}
func TestChangedEmptyProposalEqualsMissingFile(t *testing.T) {
	cfg := &config.Config{Learning: defaults(t)}
	if Changed(cfg, Propose(cfg, nil, nil)) {
		t.Fatal("empty proposal changed")
	}
}
func TestProfileWinnerJoinsLadder(t *testing.T) {
	cfg := &config.Config{Learning: defaults(t)}
	vs := decidedScores([]record.Score{{Criterion: "correct", Original: 1, Profile: 3}}, nil, "confirm")
	p := Propose(cfg, Aggregate(cfg, nil, nil, vs), vs)
	if !slices.Contains(p.Ladders["debug"].Order, "claude/sonnet/high") {
		t.Fatal(p)
	}
}
func TestLineDiff(t *testing.T) {
	if got := LineDiff([]byte("a\nb\n"), []byte("a\nc\n")); got != " a\n-b\n+c\n" {
		t.Fatalf("%q", got)
	}
	if got := LineDiff([]byte("a\n"), []byte("a\n")); got != "" {
		t.Fatal(got)
	}
}
func TestEvidence(t *testing.T) {
	st := Stats{"debug": {"codex/sol/medium": {Delegations: 4, Weight: 2.5, Quality: 2.8, USD: 0.31, Seconds: 95}}}
	want := "debug: codex/sol/medium 4 delegations, weight 2.5, quality 2.80, failures 0, $0.31, 95s"
	if got := Evidence(st, "debug"); !slices.Equal(got, []string{want}) {
		t.Fatal(got)
	}
}

func TestRankCostUsesObservedPrices(t *testing.T) {
	l := defaults(t)
	c := l.Classes["tests"]
	st := map[string]config.EntryStat{}
	for i, e := range c.Ladder {
		st[e.Key()] = config.EntryStat{Delegations: 5, Weight: 5, Quality: 3, USD: float64(2 - i)}
	}
	if got := Rank(l, c, c.Ladder, st); got[0] != c.Ladder[1].Key() {
		t.Fatal(got)
	}
}
func TestProposeStepAndExistingOutsideEntry(t *testing.T) {
	l := defaults(t)
	cfg := &config.Config{Learning: l}
	key := "skeleton.plan"
	outside := "claude/sonnet/high"
	cfg.Learning.Learned = map[string]config.LearnedLadder{key: {Order: []string{outside}}}
	vs := decidedScores([]record.Score{{Criterion: "a", Original: 1, Profile: 3}}, nil, "confirm")
	vs[0].Ladder = key
	p := Propose(cfg, nil, vs)
	if !slices.Contains(p.Ladders[key].Order, outside) {
		t.Fatal(p)
	}
	st := Stats{key: {"codex/sol/high": {Weight: 20, Quality: 3}, "claude/opus/high": {Weight: 20, Quality: 0}}}
	p = Propose(cfg, st, vs)
	if p.Ladders[key].Order[0] != "codex/sol/high" || !Changed(cfg, p) {
		t.Fatal(p)
	}
}

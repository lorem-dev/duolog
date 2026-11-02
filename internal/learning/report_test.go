package learning

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
)

func reportConfig(t *testing.T) *config.Config {
	t.Helper()
	defaults(t)
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
func reportRun(id, class, role, entry string, step int) journal.Delegation {
	return journal.Delegation{ID: id, Task: "t1", Class: class, Ladder: class, Role: role, Entry: entry, Step: step, USD: 1, Priced: true, Done: true}
}
func TestOffersAtMostTwo(t *testing.T) {
	cfg := reportConfig(t)
	ds := []journal.Delegation{reportRun("d3", "tests", "tests", "codex/terra/medium", 3), reportRun("d1", "debug", "debug_hard", "codex/terra/medium", 1), reportRun("d2", "debug", "debug_hard", "codex/terra/medium", 2)}
	got := Offers(cfg, ds, nil, nil)
	if len(got) != 2 || got[0].Delegation != "d1" || got[1].Delegation != "d2" {
		t.Fatal(got)
	}
	if got[0].Ladder == got[1].Ladder && got[0].To == got[1].To {
		t.Fatal(got)
	}
}
func TestOfferPrefersCheaperCloseEntry(t *testing.T) {
	cfg := reportConfig(t)
	d := reportRun("d1", "debug", "debug_hard", "codex/sol/medium", 1)
	st := Stats{"debug": {"codex/sol/medium": {Delegations: 2, USD: 1}, "codex/terra/medium": {Delegations: 2, USD: 0.2}}}
	got := Offers(cfg, []journal.Delegation{d}, st, nil)
	if len(got) != 1 || got[0].To != "codex/terra/medium" || got[0].Reason != "cheaper and close to the best" {
		t.Fatal(got)
	}
}
func TestOfferEstimateUsesPriceRatio(t *testing.T) {
	cfg := reportConfig(t)
	d := reportRun("d1", "review", "review_final", "codex/sol/medium", 1)
	got := Offers(cfg, []journal.Delegation{d}, nil, nil)
	if len(got) != 1 || got[0].USD != 4 {
		t.Fatal(got)
	}
}
func TestJudgeTargetOppositeVendor(t *testing.T) {
	l := defaults(t)
	for _, tc := range []struct{ a, b, want string }{{"codex", "codex", "claude/opus/high"}, {"claude", "claude", "codex/sol/high"}, {"codex", "claude", "claude/opus/high"}} {
		got := JudgeTarget(l, config.Target{Agent: tc.a}, config.Target{Agent: tc.b})
		e := config.LadderEntry{Agent: got.Agent, Model: got.Model, Effort: got.Effort}
		if e.Key() != tc.want {
			t.Fatal(e)
		}
	}
}
func TestUnclassifiedGetsNoOffer(t *testing.T) {
	cfg := reportConfig(t)
	for _, kind := range []string{"unclassified", "infra", "lead", "replay"} {
		d := reportRun("d1", "debug", "debug_hard", "codex/terra/medium", 1)
		switch kind {
		case "unclassified":
			d.Class = Unclassified
		case "infra", "lead":
			d.Outcome = kind
		case "replay":
			d.ProfileOf = "original"
		}
		if got := Offers(cfg, []journal.Delegation{d}, nil, nil); len(got) != 0 {
			t.Fatal(kind, got)
		}
	}
}
func TestPersonChoiceGetsNoOffer(t *testing.T) {
	cfg := reportConfig(t)
	d := reportRun("d1", "debug", "debug_hard", "codex/terra/medium", 1)
	d.Why = "person"
	if got := Offers(cfg, []journal.Delegation{d}, nil, nil); len(got) != 0 {
		t.Fatal(got)
	}
}
func TestOfferUsesObservedReplayAndJudgeMeans(t *testing.T) {
	cfg := reportConfig(t)
	d := reportRun("d1", "review", "review_final", "codex/sol/medium", 1)
	st := Stats{"review": {"claude/opus/high": {Delegations: 1, USD: 0.5}}}
	vs := []journal.Verdict{{Kind: "verdict", JudgeUSD: 0.2}, {Kind: "decision", JudgeUSD: 99}, {Kind: "verdict", JudgeUSD: 0.4}}
	got := Offers(cfg, []journal.Delegation{d}, st, vs)
	if len(got) != 1 || got[0].USD != 0.8 {
		t.Fatal(got)
	}
}
func TestTableMarksEscalated(t *testing.T) {
	d := reportRun("d1", "debug", "debug_hard", "codex/terra/medium", 1)
	e := reportRun("d2", "debug", "debug_hard", "codex/sol/medium", 2)
	e.EscalatedFrom = d.ID
	var b bytes.Buffer
	if err := WriteTable(&b, []journal.Delegation{d, e}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if !strings.HasSuffix(lines[1], "escalated") {
		t.Fatal(b.String())
	}
}
func TestTableSkipsReplaysAndMarksInfra(t *testing.T) {
	d := reportRun("d1", "debug", "debug_hard", "codex/terra/medium", 1)
	d.Outcome = "infra"
	d.Done = false
	p := d
	p.Entry = "replay-only"
	p.ProfileOf = d.ID
	var b bytes.Buffer
	if err := WriteTable(&b, []journal.Delegation{d, p}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "replay-only") || !strings.HasSuffix(strings.TrimSpace(b.String()), "infra") {
		t.Fatal(b.String())
	}
}
func TestTableOutcomes(t *testing.T) {
	for _, want := range []string{"done", "needs", "failed", "run here"} {
		d := reportRun("d1", "debug", "debug_hard", "codex/terra/medium", 1)
		switch want {
		case "needs":
			d.Done = false
			d.Outcome = "lead"
		case "failed":
			d.Done = false
		case "run here":
			d.RunHere = true
		}
		var b bytes.Buffer
		if err := WriteTable(&b, []journal.Delegation{d}); err != nil || !strings.HasSuffix(strings.TrimSpace(b.String()), want) {
			t.Fatal(want, b.String(), err)
		}
	}
}
func TestWriteReportFixedWording(t *testing.T) {
	cfg := reportConfig(t)
	d := reportRun("d1", "review", "review_final", "codex/sol/medium", 1)
	var b bytes.Buffer
	err := WriteReport(&b, cfg, journal.OpenTask{ID: "t1"}, []journal.Delegation{d}, Offers(cfg, []journal.Delegation{d}, nil, nil), true, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"task t1: 1 delegations, $1.00 (estimated)", "Profiling offers (the user picks one, both or none):", "1. review review_final on claude/opus/high instead of codex/sol/medium: few judged runs in that cell; about $4.00. Run: duolog profile d1 --entry claude/opus/high", "duolog learn has a proposed change; show it to the user after the outcome.", "A new class proposal is ready: run duolog learn after the outcome.", "Ask the user for the outcome: accepted, small_fixes, reworked or dropped.", "Record it with: duolog task outcome t1 <outcome>"} {
		if !strings.Contains(b.String(), line+"\n") {
			t.Fatal(line, b.String())
		}
	}
	b.Reset()
	if err := WriteReport(&b, cfg, journal.OpenTask{ID: "t1"}, nil, nil, false, false); err != nil || !strings.Contains(b.String(), "Profiling offers: none\n") {
		t.Fatal(b.String(), err)
	}
}

type reportBadWriter struct{}

func (reportBadWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func TestReportPropagatesWriterErrors(t *testing.T) {
	cfg := reportConfig(t)
	if err := WriteTable(reportBadWriter{}, nil); err == nil {
		t.Fatal("table swallowed error")
	}
	if err := WriteReport(reportBadWriter{}, cfg, journal.OpenTask{}, nil, nil, false, false); err == nil {
		t.Fatal("report swallowed error")
	}
}

func TestOfferSkipsUnpricedCandidateAndContinues(t *testing.T) {
	cfg := reportConfig(t)
	delete(cfg.Models, "sol")
	d := reportRun("d1", "debug", "debug_hard", "codex/terra/medium", 1)
	got := Offers(cfg, []journal.Delegation{d}, nil, nil)
	if len(got) != 1 || got[0].To != "claude/opus/high" {
		t.Fatal(got)
	}
}

func TestOffersWithoutJudge(t *testing.T) {
	cfg := reportConfig(t)
	cfg.Learning.Judges = nil
	if got := JudgeTarget(cfg.Learning, config.Target{}, config.Target{}); got != (config.Target{}) {
		t.Fatal(got)
	}
	d := reportRun("d1", "debug", "debug_hard", "codex/terra/medium", 1)
	if got := Offers(cfg, []journal.Delegation{d}, nil, nil); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestOfferOnlyNeedsPricesForRatios(t *testing.T) {
	cfg := reportConfig(t)
	cfg.Models = nil
	d := reportRun("d1", "review", "review_final", "codex/sol/medium", 1)
	st := Stats{"review": {"claude/opus/high": {Delegations: 1, USD: 0.5}}}
	vs := []journal.Verdict{{Kind: "verdict", JudgeUSD: 0.25}}
	got := Offers(cfg, []journal.Delegation{d}, st, vs)
	if len(got) != 1 || got[0].USD != 0.75 {
		t.Fatal(got)
	}
	if got := Offers(cfg, []journal.Delegation{d}, nil, vs); len(got) != 0 {
		t.Fatal(got)
	}
}

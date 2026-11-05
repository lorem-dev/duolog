package learning

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
)

const (
	calA = "claude/opus/high"
	calB = "codex/sol/medium"
	calC = "codex/terra/medium"
)

var calTime = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func calResult(run, task, class, entry string, passed bool) journal.CalibrationResult {
	return journal.CalibrationResult{Kind: "result", Time: calTime, Run: run, Task: task, Class: class, Ladder: class,
		Entry: entry, Verification: "hidden", Passed: passed, Source: "calibration"}
}

// calVerdict is a calibration verdict on a task, A against B with one
// criterion, and its decision when decision is not empty.
func calVerdict(id, run, task, a, b string, sa, sb int, decision string) []journal.Verdict {
	vs := []journal.Verdict{{Kind: "verdict", ID: id, Source: "calibration", Run: run, Task: task, Class: "tests", Ladder: "tests",
		Original: a, Profile: b, Scores: []journal.Score{{Criterion: "a", Original: sa, Profile: sb}}}}
	if decision != "" {
		vs = append(vs, journal.Verdict{Kind: "decision", ID: id, Decision: decision})
	}
	return vs
}

func TestAggregateCheckFailureCountsWithoutConfirmation(t *testing.T) {
	cs := []journal.CalibrationResult{calResult("r1", "t1", "tests", calC, false)}
	s := AggregateAll(nil, nil, nil, nil, cs)["tests"][calC]
	if s.Quality != 0 || s.Weight != 1 || s.CalibrationTasks != 1 {
		t.Fatal(s)
	}
}

func TestAggregateOneObservationPerTaskEntry(t *testing.T) {
	cs := []journal.CalibrationResult{
		calResult("r1", "t1", "tests", calA, true),
		calResult("r1", "t1", "tests", calB, true),
		calResult("r1", "t1", "tests", calC, true),
	}
	vs := slices.Concat(calVerdict("v1", "r1", "t1", calA, calB, 3, 1, "confirm"), calVerdict("v2", "r1", "t1", calA, calC, 2, 2, "confirm"))
	st := AggregateAll(nil, nil, nil, vs, cs)["tests"]
	if s := st[calA]; s.Weight != 1 || s.Quality != 2.5 || s.CalibrationTasks != 1 {
		t.Fatal(s)
	}
	if s := st[calB]; s.Weight != 1 || s.Quality != 1 {
		t.Fatal(s)
	}
}

func TestAggregateJudgeOnlyHalfWeight(t *testing.T) {
	cs := []journal.CalibrationResult{calResult("r1", "t1", "tests", calA, true), calResult("r1", "t1", "tests", calB, true)}
	for i := range cs {
		cs[i].Verification = "judge_only"
	}
	vs := calVerdict("v1", "r1", "t1", calA, calB, 3, 1, "confirm")
	st := AggregateAll(nil, nil, nil, vs, cs)["tests"]
	if s := st[calA]; s.Weight != 0.5 || s.Quality != 3 {
		t.Fatal(s)
	}
}

func TestAggregateUnconfirmedVerdictIgnored(t *testing.T) {
	cs := []journal.CalibrationResult{calResult("r1", "t1", "tests", calA, true), calResult("r1", "t1", "tests", calB, true)}
	vs := calVerdict("v1", "r1", "t1", calA, calB, 3, 1, "")
	s := AggregateAll(nil, nil, nil, vs, cs)["tests"][calA]
	// Both passed, so the task waits for the person's verdict before it
	// counts toward the three.
	if s.Weight != 0 || s.Quality != 0 || s.CalibrationTasks != 0 {
		t.Fatal(s)
	}
}

func TestAggregateLatestRunReplacesClass(t *testing.T) {
	r1 := []journal.CalibrationResult{
		calResult("r1", "t1", "implement", calC, false),
		calResult("r1", "t2", "implement", calC, false),
		calResult("r1", "t3", "tests", calC, false),
	}
	// terra's pass is evidence because sol failed the same task.
	r2 := []journal.CalibrationResult{calResult("r2", "t4", "implement", calC, true), calResult("r2", "t4", "implement", calB, false)}
	r2[0].Time = calTime.Add(24 * time.Hour)
	r2[1].Time = r2[0].Time
	st := AggregateAll(nil, nil, nil, nil, slices.Concat(r1, r2))
	if s := st["implement"][calC]; s.CalibrationTasks != 1 || s.Weight != 0 {
		t.Fatal("implement", s)
	}
	if s := st["tests"][calC]; s.CalibrationTasks != 1 || s.Weight != 1 || s.Quality != 0 {
		t.Fatal("tests", s)
	}
}

func TestAggregateDroppedTaskIgnored(t *testing.T) {
	cs := []journal.CalibrationResult{
		calResult("r1", "t1", "tests", calA, true),
		calResult("r1", "t1", "tests", calB, true),
		calResult("r1", "t2", "tests", calB, false),
		{Kind: "drop", Time: calTime, Run: "r1", Task: "t1", Source: "calibration"},
	}
	vs := calVerdict("v1", "r1", "t1", calA, calB, 3, 1, "confirm")
	st := AggregateAll(nil, nil, nil, vs, cs)["tests"]
	if _, ok := st[calA]; ok {
		t.Fatal("dropped task counted", st)
	}
	if s := st[calB]; s.CalibrationTasks != 1 || s.Weight != 1 || s.Quality != 0 {
		t.Fatal(s)
	}
}

func TestAggregateLiveUnchanged(t *testing.T) {
	ds := []journal.Delegation{live()}
	vs := decidedScores([]journal.Score{{Criterion: "a", Original: 3, Profile: 1}}, nil, "confirm")
	withCal := slices.Concat(vs, calVerdict("v2", "r1", "t1", calA, calB, 3, 1, "confirm"))
	want := Aggregate(nil, ds, accepted(), vs)
	if got := Aggregate(nil, ds, accepted(), withCal); !reflect.DeepEqual(got, want) {
		t.Fatalf("calibration verdict in the live loop: %v, want %v", got, want)
	}
	if got := AggregateAll(nil, ds, accepted(), withCal, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("AggregateAll(nil) = %v, want %v", got, want)
	}
}

// failedTerra has terra fail and sol pass n tests tasks.
func failedTerra(n int) []journal.CalibrationResult {
	var cs []journal.CalibrationResult
	for i := range n {
		task := fmt.Sprintf("t%d", i+1)
		cs = append(cs, calResult("r1", task, "tests", calC, false), calResult("r1", task, "tests", calB, true))
	}
	return cs
}

func TestProposeGatedNeedsThreeTasks(t *testing.T) {
	cfg := &config.Config{Learning: defaults(t)}
	p, st, kept := ProposeGated(cfg, nil, nil, nil, failedTerra(2), nil)
	if Changed(cfg, p) || kept["tests"] != "no difference found" {
		t.Fatalf("2 tasks: order %v, kept %v", p.Ladders["tests"].Order, kept)
	}
	if st["tests"][calC].CalibrationTasks != 2 {
		t.Fatal(st)
	}
	p, _, kept = ProposeGated(cfg, nil, nil, nil, failedTerra(3), nil)
	if got := p.Ladders["tests"].Order; len(got) == 0 || got[0] != calB || len(kept) != 0 {
		t.Fatalf("3 tasks: order %v, kept %v", got, kept)
	}
}

// TestCalibrationTasksNeedEvidence has two entries pass 3 tasks each while
// the person decided only the verdict on t1: each entry has one task with
// evidence, so the gate keeps the order whatever that one verdict says. A
// pass that no other entry matched is evidence without a verdict.
func TestCalibrationTasksNeedEvidence(t *testing.T) {
	cfg := &config.Config{Learning: defaults(t)}
	var cs []journal.CalibrationResult
	var vs []journal.Verdict
	for i := range 3 {
		task := fmt.Sprintf("t%d", i+1)
		cs = append(cs, calResult("r1", task, "tests", calC, true), calResult("r1", task, "tests", calB, true))
		decision := ""
		if i == 0 {
			decision = "confirm"
		}
		vs = append(vs, calVerdict("v"+task, "r1", task, calC, calB, 1, 3, decision)...)
	}
	cs = append(cs, calResult("r1", "t4", "tests", calB, true), calResult("r1", "t4", "tests", calC, false))
	p, st, kept := ProposeGated(cfg, nil, nil, vs, cs, nil)
	if s := st["tests"][calB]; s.CalibrationTasks != 2 {
		t.Fatalf("sol stat %+v, want 2 tasks with evidence (t1 judged, t4 the only pass)", s)
	}
	if s := st["tests"][calC]; s.CalibrationTasks != 2 {
		t.Fatalf("terra stat %+v, want 2 tasks with evidence (t1 judged, t4 failed)", s)
	}
	if Changed(cfg, p) || kept["tests"] != NoDifference {
		t.Fatalf("order %v moved on one decided verdict", p.Ladders["tests"].Order)
	}
}

// TestLonePassNeedsAnotherResult has sol pass t1 with no other entry's
// result on it, as when the others were cut to cap, and pass t2 where terra
// failed: only t2 compares anything, so only t2 is evidence for sol.
func TestLonePassNeedsAnotherResult(t *testing.T) {
	cs := []journal.CalibrationResult{
		calResult("r1", "t1", "tests", calB, true),
		calResult("r1", "t2", "tests", calB, true),
		calResult("r1", "t2", "tests", calC, false),
	}
	st := AggregateAll(nil, nil, nil, nil, cs)["tests"]
	if s := st[calB]; s.CalibrationTasks != 1 {
		t.Fatalf("sol stat %+v, want 1 task with evidence (t2)", s)
	}
	if s := st[calC]; s.CalibrationTasks != 1 {
		t.Fatalf("terra stat %+v, want 1 task with evidence (t2 failed)", s)
	}
}

func TestProposeGatedSeedOnlyWithoutPersonal(t *testing.T) {
	cfg := &config.Config{Learning: defaults(t)}
	seed := Stats{"tests": {calC: {Weight: 3, Quality: 0, CalibrationTasks: 3}, calB: {CalibrationTasks: 3}}}
	p, _, _ := ProposeGated(cfg, nil, nil, nil, nil, seed)
	if got := p.Ladders["tests"].Order; len(got) == 0 || got[0] != calB {
		t.Fatalf("seed without personal results: order %v", got)
	}
	cs := []journal.CalibrationResult{calResult("r1", "t1", "tests", calB, true)}
	p, st, _ := ProposeGated(cfg, nil, nil, nil, cs, seed)
	// The lone pass compares nothing, so it is no task with evidence;
	// the seed's 3 would show if it were added.
	if Changed(cfg, p) || st["tests"][calC].Weight != 0 || st["tests"][calB].CalibrationTasks != 0 {
		t.Fatalf("seed counted with personal results: order %v, stats %v", p.Ladders["tests"].Order, st)
	}
}

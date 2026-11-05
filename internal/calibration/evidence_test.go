package calibration

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/learning"
)

func TestRecordResultsIdempotent(t *testing.T) {
	j, err := journal.Open(stateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	run := &Run{ID: "cal-1", Cells: []Cell{
		{ID: "c001", Task: "t1", Class: "docs", Entry: "claude/sonnet/medium", State: CellDone, Passed: true},
		{ID: "c002", Task: "t1", Class: "docs", Entry: "codex/terra/medium", State: CellDone},
		{ID: "c003", Task: "t2", Class: "docs", Entry: "codex/terra/medium", State: CellLeakage, Passed: true},
		{ID: "c004", Task: "t2", Class: "docs", Entry: "claude/sonnet/medium", State: CellFailedLimit},
		{ID: "c005", Task: "t3", Class: "docs", Entry: "claude/sonnet/medium", State: CellInvalid},
		{ID: "c006", Task: "t3", Class: "docs", Entry: "codex/terra/medium", State: CellSkipped},
		{ID: "c007", Task: "t2", Class: "docs", Entry: "codex/sol/medium", State: CellPending},
	}}
	m := Manifest{Tasks: []ManifestTask{{ID: "t1", Class: "docs", Verification: "judge_only"}, {ID: "t2", Class: "docs", Verification: "check"}}}
	for range 2 {
		if err := RecordResults(j, testConfig(t), run, m, testTime); err != nil {
			t.Fatal(err)
		}
	}
	cs, err := journal.ReadAll[journal.CalibrationResult](j, journal.CalibrationFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("%d results, want one per done (task, entry): %+v", len(cs), cs)
	}
	for _, c := range cs {
		if c.Kind != "result" || c.Run != "cal-1" || c.Task != "t1" || c.Class != "docs" || c.Ladder != "docs" || c.Verification != "judge_only" ||
			c.Source != "calibration" || !c.Time.Equal(testTime) || c.Passed != (c.Entry == "claude/sonnet/medium") {
			t.Fatalf("result %+v", c)
		}
	}
}

func TestRecordResultsUsesStepLadder(t *testing.T) {
	j, err := journal.Open(stateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	run := &Run{ID: "cal-1", Cells: []Cell{
		// Failed, so each is evidence alone; a lone pass would need another
		// entry's result on its task.
		{ID: "c001", Task: "t1", Class: "skeleton", Entry: "claude/opus/high", State: CellDone},
		{ID: "c002", Task: "t2", Class: "skeleton", Entry: "claude/opus/high", State: CellDone},
	}}
	// t1 runs as plan, which has a step ladder in skeleton; t2 as implement, which has none.
	m := Manifest{Tasks: []ManifestTask{
		{ID: "t1", Class: "skeleton", Verification: "check", Features: map[string]string{"output": "plan"}},
		{ID: "t2", Class: "skeleton", Verification: "check", Features: map[string]string{"output": "code"}},
	}}
	if err := RecordResults(j, cfg, run, m, testTime); err != nil {
		t.Fatal(err)
	}
	cs, err := journal.ReadAll[journal.CalibrationResult](j, journal.CalibrationFile)
	if err != nil {
		t.Fatal(err)
	}
	ladders := map[string]string{}
	for _, c := range cs {
		ladders[c.Task] = c.Ladder
	}
	if ladders["t1"] != "skeleton.plan" || ladders["t2"] != "skeleton" {
		t.Fatalf("ladders = %v, want t1 skeleton.plan and t2 skeleton", ladders)
	}
	st := learning.AggregateAll(cfg, nil, nil, nil, cs)
	if st["skeleton.plan"]["claude/opus/high"].CalibrationTasks != 1 || st["skeleton"]["claude/opus/high"].CalibrationTasks != 1 {
		t.Fatalf("stats = %+v, want one task under each ladder", st)
	}
}

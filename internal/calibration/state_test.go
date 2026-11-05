package calibration

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunSaveLoadRoundTrip(t *testing.T) {
	dir := stateDir(t)
	run, err := NewRun(dir, testTime)
	if err != nil {
		t.Fatal(err)
	}
	run.Base = "0123456789abcdef0123456789abcdef01234567"
	run.Step = "run"
	run.Approved = &Approval{Hash: "sha256:abc", Time: testTime.Add(time.Minute)}
	run.Cells = append(run.Cells,
		Cell{ID: "c1", Task: "t1", Class: "simple_impl", Entry: "claude/sonnet/medium", Round: 1, State: CellDone, Passed: true, Tokens: 1200, USD: 0.4, Seconds: 61.5, Weekly: 0.5, Window: 2},
		Cell{ID: "c2", Task: "t1", Class: "simple_impl", Entry: "codex/sol/high", Round: 1, State: CellRunning, Tree: "/tmp/duolog-cal-x"},
	)
	run.Spend = Spend{
		WeeklyPct: map[string]float64{"claude": 0.5},
		Windows:   map[string]WindowSpend{"claude": {ResetsAt: testTime.Add(5 * time.Hour), StartUsed: 10, LastUsed: 12}},
	}
	if err := run.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRun(dir, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, run) {
		t.Fatalf("loaded run differs:\n got %+v\nwant %+v", loaded, run)
	}
	path := filepath.Join(RunsDir(dir), run.ID, "run.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("run.json mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestLatestRunPicksNewest(t *testing.T) {
	dir := stateDir(t)
	if _, err := NewRun(dir, testTime); err != nil {
		t.Fatal(err)
	}
	second, err := NewRun(dir, testTime.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	latest, ok, err := LatestRun(dir)
	if err != nil || !ok {
		t.Fatalf("LatestRun = %v, %v", ok, err)
	}
	if latest.ID != second.ID {
		t.Fatalf("LatestRun = %s, want %s", latest.ID, second.ID)
	}
}

func TestLockRefusesSecondHolder(t *testing.T) {
	dir := stateDir(t)
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := Lock(dir); err == nil || !strings.Contains(err.Error(), "calibration is running") {
		t.Fatalf("second Lock err = %v, want calibration is running", err)
	}
}

package delegate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/core/run"
)

func TestRecordForPreflightKeepsBlockedList(t *testing.T) {
	pe := &PreflightError{Role: "implement", Blocked: []policy.Blocked{{Command: "make lint", Decision: "ask"}}, Skipped: []string{"by hand"}}
	got := RecordFor(sampleRecord("d-1", time.Unix(1, 0)), Outcome{}, pe, time.Unix(2, 0))
	if got.State != "failed" || len(got.Blocked) != 1 || got.Blocked[0].Command != "make lint" || len(got.Skipped) != 1 {
		t.Errorf("record = %+v", got)
	}
}

func sampleRecord(id string, started time.Time) Record {
	return Record{
		ID: id, Role: "implement", Lead: "lead-1", TaskPath: ".duolog/tasks/" + id + "/task.md",
		WorkRoot: "/work", PID: 4242, State: "running", Started: started,
	}
}

func TestRecordRoundTrip(t *testing.T) {
	path := filepath.Join(RecordDir(t.TempDir()), "d-abcdef.json")
	start := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	want := sampleRecord("d-abcdef", start)
	want.State = "run_here"
	want.Line = "run here: subagent model=m effort=high task=t.md"
	want.RunHere = &Placement{Where: "subagent", Model: "m", Effort: "high", Task: "t.md"}
	want.Needs = []string{"skill:x"}
	want.Ended = start.Add(time.Minute)
	if err := WriteRecord(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecord(path, func(int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("record = %+v, want %+v", got, want)
	}
}

func TestWriteRecordIsAtomic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses a read while a rename replaces the file")
	}
	dir := RecordDir(t.TempDir())
	path := filepath.Join(dir, "d-abcdef.json")
	r := sampleRecord("d-abcdef", time.Now().UTC())
	if err := WriteRecord(path, r); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	var bad error
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := ReadRecord(path, func(int) bool { return true }); err != nil {
				bad = err
				return
			}
		}
	}()
	for i := range 200 {
		r.Line = strings.Repeat("x", 1+i*50)
		if err := WriteRecord(path, r); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if bad != nil {
		t.Fatalf("a reader saw a partial file: %v", bad)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("folder holds %d entries after the writes, want only the record", len(entries))
	}
}

func TestRunningRecordWithDeadPIDIsLost(t *testing.T) {
	path := filepath.Join(RecordDir(t.TempDir()), "d-abcdef.json")
	if err := WriteRecord(path, sampleRecord("d-abcdef", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecord(path, func(pid int) bool { return pid != 4242 })
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "lost" {
		t.Fatalf("state = %q, want lost", got.State)
	}
	got, err = ReadRecord(path, func(int) bool { return true })
	if err != nil || got.State != "running" {
		t.Fatalf("state = %q, err = %v; want running for a live pid", got.State, err)
	}
	done := sampleRecord("d-abcdef", time.Now().UTC())
	done.State = "done"
	if err := WriteRecord(path, done); err != nil {
		t.Fatal(err)
	}
	got, _ = ReadRecord(path, func(int) bool { return false })
	if got.State != "done" {
		t.Fatalf("a finished record reads %q, want done", got.State)
	}
}

func TestListRecordsNewestFirst(t *testing.T) {
	dir := RecordDir(t.TempDir())
	base := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"d-aaaaaa", "d-bbbbbb", "d-cccccc"} {
		if err := WriteRecord(filepath.Join(dir, id+".json"), sampleRecord(id, base.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "d-aaaaaa.log"), []byte("log"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ListRecords(dir, func(int) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if want := []string{"d-cccccc", "d-bbbbbb", "d-aaaaaa"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if none, err := ListRecords(filepath.Join(dir, "missing"), nil); err != nil || len(none) != 0 {
		t.Fatalf("missing folder: %v, %v; want empty", none, err)
	}
}

func TestRecordForOutcomes(t *testing.T) {
	base := sampleRecord("d-abcdef", time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC))
	now := base.Started.Add(time.Minute)
	self := &Placement{Where: "self", Task: "t.md"}
	sub := &Placement{Where: "subagent", Model: "m", Effort: "high", Task: "t.md"}
	for _, tc := range []struct {
		name    string
		out     Outcome
		err     error
		state   string
		line    string
		runHere *Placement
		needs   []string
	}{
		{"done", Outcome{Line: "done ok"}, nil, "done", "done ok", nil, nil},
		{"needs", Outcome{Line: "needs skill", Needs: []string{"skill:x"}}, nil, "needs", "needs skill", nil, []string{"skill:x"}},
		{"run here self", Outcome{Line: self.String(), RunHere: self}, nil, "run_here", self.String(), self, nil},
		{"run here subagent", Outcome{Line: sub.String(), RunHere: sub}, nil, "run_here", sub.String(), sub, nil},
		{"not started", Outcome{}, &NotStartedError{Err: errors.New("route refused")}, "failed", "route refused", nil, nil},
		{"other error", Outcome{Line: "partial"}, errors.New("worker crashed"), "failed", "worker crashed", nil, nil},
	} {
		got := RecordFor(base, tc.out, tc.err, now)
		if got.State != tc.state || got.Line != tc.line || !reflect.DeepEqual(got.RunHere, tc.runHere) || !reflect.DeepEqual(got.Needs, tc.needs) {
			t.Errorf("%s: got %+v", tc.name, got)
		}
		if !got.Ended.Equal(now) || got.ID != base.ID || got.TaskPath != base.TaskPath || got.Lead != base.Lead || !got.Started.Equal(base.Started) {
			t.Errorf("%s: base fields lost: %+v", tc.name, got)
		}
	}
}

func TestRecordForKeepsTheWorkersAnswer(t *testing.T) {
	base := sampleRecord("d-abcdef", time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC))
	res := run.Result{Done: true, Summary: "Fixed it.", Files: []string{"a.go", "b.go"}, Decisions: []string{"used maps"}}
	got := RecordFor(base, Outcome{Line: "done=true", Result: res}, nil, base.Started)
	if !got.Done || got.Summary != "Fixed it." || !slices.Equal(got.Files, res.Files) || !slices.Equal(got.Decisions, res.Decisions) {
		t.Fatalf("%+v", got)
	}
	// Done is the worker's word, not the state's: a needs record can be false.
	got = RecordFor(base, Outcome{Result: run.Result{Done: false, Summary: "Blocked."}, Needs: []string{"mcp:x"}}, nil, base.Started)
	if got.Done || got.State != "needs" || got.Summary != "Blocked." {
		t.Fatalf("%+v", got)
	}
	// A failure keeps none of a partial result.
	got = RecordFor(base, Outcome{Result: res}, errors.New("boom"), base.Started)
	if got.Done || got.Summary != "" || got.Files != nil {
		t.Fatalf("%+v", got)
	}
}

func TestParsePlacementRoundTrip(t *testing.T) {
	for _, p := range []Placement{
		{Where: "self", Task: ".duolog/tasks/d-abcdef/task.md"},
		{Where: "subagent", Model: "claude-sonnet", Effort: "high", Task: ".duolog/tasks/d-abcdef/task.md"},
		{Where: "self", Task: "a task.md"},
	} {
		got, ok := ParsePlacement(p.String())
		if !ok || got.Where != p.Where || got.Task != p.Task || (p.Where == "subagent" && (got.Model != p.Model || got.Effort != p.Effort)) {
			t.Errorf("ParsePlacement(%q) = %+v, %v", p.String(), got, ok)
		}
	}
	for _, line := range []string{"", "done ok", "run here: elsewhere task=x", "run here: subagent model=m task=x"} {
		if p, ok := ParsePlacement(line); ok {
			t.Errorf("ParsePlacement(%q) = %+v, want no", line, p)
		}
	}
}

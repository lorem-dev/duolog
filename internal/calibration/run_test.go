package calibration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// fakeQuota answers every agent with one weekly and one five-hour window,
// then the model windows. failAt makes that read and every later one fail;
// noReset leaves the five-hour window without a reset time.
type fakeQuota struct {
	weekly, window float64
	reset          time.Time
	reads          int
	failAt         int
	noReset        bool
	models         []Window
}

func (q *fakeQuota) read(ctx context.Context, agent string) ([]Window, error) {
	q.reads++
	if q.failAt > 0 && q.reads >= q.failAt {
		return nil, errors.New("quota endpoint down")
	}
	reset := q.reset
	if q.noReset {
		reset = time.Time{}
	}
	return append([]Window{
		{Kind: KindWeekly, UsedPercent: q.weekly, ResetsAt: q.reset.Add(72 * time.Hour)},
		{Kind: KindFiveHour, UsedPercent: q.window, ResetsAt: reset},
	}, q.models...), nil
}

type runFixture struct {
	stateDir, runDir string
	run              *Run
	quota            *fakeQuota
	calls            []string // "rewrite:<task>" and "cell:<task>"
	out              bytes.Buffer
	runner           *Runner
	// step is what each cell adds to the weekly window.
	step float64
}

const runEstimate = 1_000_000

// newRunFixture approves a plan of class implement whose cells run every
// entry on every task, in that order; the cells pass with 1M tokens each.
func newRunFixture(t *testing.T, tasks, entries []string) *runFixture {
	t.Helper()
	cfg := testConfig(t)
	f := &runFixture{stateDir: t.TempDir(), quota: &fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}}
	f.run = &Run{ID: "cal-1", Step: "plan", Created: testTime, Approved: &Approval{Hash: "h", Time: testTime}}
	f.runDir = f.run.Dir(f.stateDir)
	m := Manifest{Estimates: map[string]Estimate{}, Reserve: map[string][]string{}, Verification: map[string]string{}}
	for _, id := range tasks {
		f.addTask(t, &m, id)
		m.Tasks = append(m.Tasks, ManifestTask{ID: id, Class: "implement", Statement: "Add " + id + ".", Verification: "hidden_tests"})
	}
	var planEntries []PlanEntry
	for _, e := range entries {
		planEntries = append(planEntries, PlanEntry{Key: e})
	}
	cells := len(tasks) * len(entries)
	p := Plan{Run: f.run.ID, CapWeekly: 10, CapWindow: 50, Classes: []ClassPlan{{
		Class: "implement", Role: "implement", Tasks: slices.Clone(tasks), Entries: planEntries, Cells: cells,
		TokensLow: int64(cells) * runEstimate / 4, TokensHigh: int64(cells) * runEstimate,
	}}}
	for _, task := range tasks {
		for _, e := range entries {
			f.run.Cells = append(f.run.Cells, Cell{ID: fmt.Sprintf("c%03d", len(f.run.Cells)+1), Task: task, Class: "implement", Entry: e, Round: 1, State: CellPending})
		}
	}
	if err := f.run.Save(f.stateDir); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(f.runDir, m); err != nil {
		t.Fatal(err)
	}
	// A rewrite reads the history's names, so the project is a repository.
	root := project(t)
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	f.runner = &Runner{
		StateDir: f.stateDir, Root: root, Cfg: cfg, Run: f.run, Plan: p, Manifest: m,
		Quota: &QuotaCache{Read: f.quota.read, Now: func() time.Time { return testTime }},
		Now:   func() time.Time { return testTime },
		Out:   &f.out,
	}
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		f.calls = append(f.calls, "cell:"+task.ID)
		f.quota.weekly += f.step
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate, USD: 1, Seconds: 120}, nil
	})
	f.rewrite(func(task TaskRecord) (string, int64, error) {
		f.calls = append(f.calls, "rewrite:"+task.ID)
		return "Add a function Parse for " + task.ID + ".", 5000, nil
	})
	return f
}

// cell makes run the fixture's cell: it runs once the cell is admitted.
func (f *runFixture) cell(run func(c Cell, task TaskRecord) (CellResult, error)) {
	f.runner.Cell = func(ctx context.Context, c Cell, task TaskRecord, start StartFunc) (CellResult, error) {
		if err := StartStep(start); err != nil {
			return CellResult{}, err
		}
		return run(c, task)
	}
}

// rewrite makes run the fixture's rewrite: it runs once it is admitted.
func (f *runFixture) rewrite(run func(task TaskRecord) (string, int64, error)) {
	f.runner.Rewrite = func(ctx context.Context, task TaskRecord, start StartFunc) (string, int64, error) {
		if err := StartStep(start); err != nil {
			return "", 0, err
		}
		return run(task)
	}
}

// addTask writes the task files of id with an estimate of runEstimate tokens.
func (f *runFixture) addTask(t *testing.T, m *Manifest, id string) {
	t.Helper()
	m.Estimates[id] = Estimate{Files: 1, Lines: 20, TokensLow: runEstimate / 4, TokensHigh: runEstimate, Minutes: 5}
	m.Verification[id] = "hidden_tests"
	rec := TaskRecord{
		ManifestTask: ManifestTask{ID: id, Class: "implement", Statement: "Add " + id + ".", Verification: "hidden_tests"},
		Parent:       strings.Repeat("1", 40), Ref: strings.Repeat("2", 40), Subject: "add the " + id + " parser",
		Commits: []string{"abcdef0123456789abcdef0123456789abcdef01"}, HiddenNames: []string{"Parse"},
	}
	if err := writeJSON(filepath.Join(f.runDir, TasksDir, id, "task.json"), rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.runDir, TasksDir, id, "statement.txt"), []byte(rec.Statement+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

// needsRewrite marks the tasks for a rewrite in the task files and the manifest.
func (f *runFixture) needsRewrite(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		rec, err := ReadTask(f.runDir, id)
		if err != nil {
			t.Fatal(err)
		}
		rec.NeedsRewrite = true
		if err := writeJSON(filepath.Join(f.runDir, TasksDir, id, "task.json"), rec); err != nil {
			t.Fatal(err)
		}
	}
	f.runner.Manifest.Rewrite = append(f.runner.Manifest.Rewrite, ids...)
}

func (f *runFixture) execute(t *testing.T) Outcome {
	t.Helper()
	o, err := f.runner.Execute(context.Background())
	if err != nil {
		t.Fatal(err, f.out.String())
	}
	return o
}

func (f *runFixture) saved(t *testing.T) *Run {
	t.Helper()
	r, err := LoadRun(f.stateDir, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRunRewritesFirst(t *testing.T) {
	f := newRunFixture(t, []string{"t1", "t2"}, []string{"codex/sol/medium", "codex/sol/high"})
	f.needsRewrite(t, "t1", "t2")
	o := f.execute(t)
	if !o.Done {
		t.Fatalf("outcome %+v, want done:\n%s", o, f.out.String())
	}
	firstCell := slices.IndexFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "cell:") })
	lastRewrite := -1
	for i, c := range f.calls {
		if strings.HasPrefix(c, "rewrite:") {
			lastRewrite = i
		}
	}
	if lastRewrite != 1 || firstCell != 2 {
		t.Fatalf("calls %v, want both rewrites before every cell", f.calls)
	}
	data, err := os.ReadFile(filepath.Join(f.runDir, TasksDir, "t1", "statement.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "Add a function Parse for t1." {
		t.Fatalf("statement.txt = %q", data)
	}
	rec, err := ReadTask(f.runDir, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.NeedsRewrite || rec.Statement != "Add a function Parse for t1." {
		t.Fatalf("task.json %+v, want the rewritten statement", rec)
	}
}

func TestRunRewriteRefusedUsesReserve(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium", "codex/sol/high"})
	f.addTask(t, &f.runner.Manifest, "t9")
	f.runner.Manifest.Reserve["implement"] = []string{"t9"}
	f.needsRewrite(t, "t1")
	sha := "abcdef0123456789abcdef0123456789abcdef01"
	f.rewrite(func(task TaskRecord) (string, int64, error) {
		f.calls = append(f.calls, "rewrite:"+task.ID)
		return "Add Parse as in " + sha[:8] + ".", 5000, nil
	})
	f.execute(t)
	saved := f.saved(t)
	var invalid, moved []Cell
	for _, c := range saved.Cells {
		switch c.Task {
		case "t1":
			invalid = append(invalid, c)
		case "t9":
			moved = append(moved, c)
		}
	}
	if len(invalid) != 2 || len(moved) != 2 {
		t.Fatalf("cells %+v, want 2 of t1 and 2 of t9", saved.Cells)
	}
	for i, c := range invalid {
		if c.State != CellInvalid {
			t.Fatalf("t1 cell %+v, want invalid", c)
		}
		if moved[i].Entry != c.Entry || moved[i].State != CellDone {
			t.Fatalf("t9 cell %+v, want done on %s", moved[i], c.Entry)
		}
	}
	if slices.Contains(f.calls, "cell:t1") || !slices.Contains(f.calls, "cell:t9") {
		t.Fatalf("calls %v, want cells of t9 only", f.calls)
	}
}

// TestRunRewriteRefusesPrivateStatement runs the real rewrite step on a
// project whose history has an author name: a rewrite that brings back that
// name, or a figure of the person's account, is refused, and the task keeps
// its old statement and its need for a rewrite.
func TestRunRewriteRefusesPrivateStatement(t *testing.T) {
	for _, statement := range []string{
		"Add a function Parse; ask Robin Sample before you merge.",
		"Add a function Parse and keep weekly usage below 75%.",
		"Add a function Parse that spends at most $42.",
	} {
		f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
		write(t, f.runner.Root, "b.txt", "b\n")
		commitAs(t, f.runner.Root, "Robin Sample", "Casey Placeholder", "more")
		f.needsRewrite(t, "t1")
		f.rewrite(func(task TaskRecord) (string, int64, error) {
			f.calls = append(f.calls, "rewrite:"+task.ID)
			return statement, 5000, nil
		})
		f.execute(t)
		if slices.Contains(f.calls, "cell:t1") {
			t.Errorf("%q: calls %v, want no cell on a refused statement", statement, f.calls)
		}
		if !strings.Contains(f.out.String(), "rewritten statement refused") {
			t.Errorf("%q: output lacks the refusal:\n%s", statement, f.out.String())
		}
		rec, err := ReadTask(f.runDir, "t1")
		if err != nil {
			t.Fatal(err)
		}
		if !rec.NeedsRewrite || rec.Statement != "Add t1." {
			t.Errorf("%q: task.json %+v, want the old statement still needing a rewrite", statement, rec)
		}
		m, err := LoadManifest(f.runDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range m.Tasks {
			if task.Statement == statement {
				t.Errorf("%q reached the manifest", statement)
			}
		}
	}
}

// TestRunRewriteKeepsServiceNames runs the real rewrite step on a history
// with web merges and bot commits beside a person's: a statement that names
// the service is kept, while the person's name is still on the list.
func TestRunRewriteKeepsServiceNames(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	root := f.runner.Root
	for i, who := range []struct{ author, committer string }{
		{"dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com>", "GitHub <noreply@github.com>"},
		// A person's own noreply address is still a person.
		{"Robin Sample <12345+robin@users.noreply.github.com>", "GitHub <no-reply@github.com>"},
	} {
		write(t, root, fmt.Sprintf("b%d.txt", i), "b\n")
		name, email, _ := strings.Cut(strings.TrimSuffix(who.committer, ">"), " <")
		git(t, root, "add", "-A")
		git(t, root, "-c", "user.name="+name, "-c", "user.email="+email, "commit", "-q", "--author="+who.author, "-m", "merge")
	}
	names, err := HistoryNames(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names, "Robin Sample") || slices.Contains(names, "GitHub") || slices.Contains(names, "dependabot[bot]") {
		t.Fatalf("names %q, want the people without the service and bot identities", names)
	}
	const statement = "Add a function Parse that reads the GitHub Actions workflow files."
	f.needsRewrite(t, "t1")
	f.rewrite(func(task TaskRecord) (string, int64, error) {
		f.calls = append(f.calls, "rewrite:"+task.ID)
		return statement, 5000, nil
	})
	f.execute(t)
	if !slices.Contains(f.calls, "cell:t1") || strings.Contains(f.out.String(), "rewritten statement refused") {
		t.Fatalf("calls %v, want the rewrite kept and the cell run:\n%s", f.calls, f.out.String())
	}
	if rec, err := ReadTask(f.runDir, "t1"); err != nil || rec.Statement != statement {
		t.Fatalf("task.json %+v, %v; want the rewritten statement", rec, err)
	}
}

func TestRunOneLinePerCell(t *testing.T) {
	f := newRunFixture(t, []string{"t1", "t2"}, []string{"codex/sol/medium", "claude/opus/high"})
	f.execute(t)
	var lines []string
	for _, line := range strings.Split(f.out.String(), "\n") {
		if strings.HasPrefix(line, "cell ") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 4 {
		t.Fatalf("cell lines %q, want 4:\n%s", lines, f.out.String())
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, fmt.Sprintf("cell %d/4 ", i+1)) {
			t.Fatalf("line %d = %q", i+1, line)
		}
	}
	if lines[0] != "cell 1/4 implement codex/sol/medium t1: passed, 1000k tokens, 2m" {
		t.Fatalf("first line = %q", lines[0])
	}
}

func TestRunAdmissionStopsAtReserve(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium", "codex/sol/high"})
	f.quota.weekly = 84
	// A measured rate of 2 points per 1M tokens makes a cell cost 2 points.
	if err := WriteRates(f.stateDir, map[string]Rate{"codex": {WeeklyPerMTok: 2}}); err != nil {
		t.Fatal(err)
	}
	o := f.execute(t)
	if o.Stopped != "lead reserve" || o.Done {
		t.Fatalf("outcome %+v, want stopped at lead reserve", o)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v, want none", f.calls)
	}
	saved := f.saved(t)
	if saved.Stopped != "lead reserve" {
		t.Fatalf("run.json Stopped = %q", saved.Stopped)
	}
	for _, c := range saved.Cells {
		if c.State != CellPending {
			t.Fatalf("cell %+v, want pending", c)
		}
	}
}

func TestRunPilotWithoutRate(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium", "codex/sol/high"})
	f.runner.Plan.Pilot = true
	f.quota.weekly, f.step = 10, 0.5
	f.execute(t)
	if f.quota.weekly != 11 {
		t.Fatalf("weekly = %v, want 11 after 2 cells", f.quota.weekly)
	}
	rates, err := ReadRates(f.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := rates["codex"].WeeklyPerMTok; got != 0.5 {
		t.Fatalf("rates %+v, want codex WeeklyPerMTok 0.5", rates)
	}
}

func TestRunPilotReapprove(t *testing.T) {
	f := newRunFixture(t, []string{"t1", "t2"}, []string{"codex/sol/medium", "codex/sol/high"})
	f.runner.Plan.Pilot = true
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		f.calls = append(f.calls, "cell:"+task.ID)
		return CellResult{State: CellDone, Passed: true, Tokens: 2 * runEstimate, Seconds: 60}, nil
	})
	o := f.execute(t)
	if !o.Reapprove || o.Stopped != "re-approve" || o.Done {
		t.Fatalf("outcome %+v, want re-approve", o)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls %v, want the 2 pilot cells", f.calls)
	}
	if !strings.Contains(f.out.String(), "plan again: duolog calibrate plan") {
		t.Fatalf("output lacks plan again:\n%s", f.out.String())
	}
	if saved := f.saved(t); saved.Stopped != "re-approve" {
		t.Fatalf("run.json Stopped = %q", saved.Stopped)
	}
}

func TestRunPilotNoMovementKeepsBound(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium", "codex/sol/high"})
	f.runner.Plan.Pilot = true
	f.execute(t)
	if _, err := os.Stat(ratesPath(f.stateDir)); !os.IsNotExist(err) {
		t.Fatalf("rates.json exists: %v", err)
	}
	if !strings.Contains(f.out.String(), "pilot showed no measurable movement") {
		t.Fatalf("output lacks the no-movement line:\n%s", f.out.String())
	}
}

func TestRaceDropsFailingEntry(t *testing.T) {
	const e1, e2, e3 = "codex/sol/medium", "codex/sol/high", "claude/opus/high"
	passes := map[string][3]bool{e1: {true, true, true}, e2: {false, false, true}, e3: {false, true, true}}
	var cells []Cell
	for _, e := range []string{e1, e2, e3} {
		for i, task := range []string{"a", "b", "c"} {
			cells = append(cells, Cell{ID: fmt.Sprintf("c%03d", len(cells)+1), Task: task, Class: "implement", Entry: e, Round: 1, State: CellDone, Passed: passes[e][i], USD: 1})
		}
	}
	for _, e := range []string{e1, e2, e3} {
		cells = append(cells, Cell{ID: fmt.Sprintf("c%03d", len(cells)+1), Task: "d", Class: "implement", Entry: e, Round: 2, State: CellPending})
	}
	got := Race(cells, []string{e1, e2, e3})
	for _, c := range got {
		if c.Round != 2 {
			continue
		}
		switch c.Entry {
		case e2:
			if c.State != CellSkipped || c.Reason != "raced out" {
				t.Fatalf("E2 round-2 cell %+v, want skipped, raced out", c)
			}
		default:
			if c.State != CellPending {
				t.Fatalf("%s round-2 cell %+v, want pending", c.Entry, c)
			}
		}
	}
	if cells[len(cells)-2].State != CellPending {
		t.Fatal("Race changed its input")
	}
}

func TestRunStopsOnProviderLimit(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium", "codex/sol/high"})
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		f.calls = append(f.calls, "cell:"+task.ID)
		return CellResult{State: CellFailedLimit, Reason: "provider limit", Tokens: 10}, nil
	})
	o := f.execute(t)
	if o.Stopped != "provider limit" || o.Done {
		t.Fatalf("outcome %+v, want stopped at provider limit", o)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls %v, want 1", f.calls)
	}
	saved := f.saved(t)
	if saved.Stopped != "provider limit" || saved.Cells[0].State != CellFailedLimit || saved.Cells[1].State != CellPending {
		t.Fatalf("run.json %+v", saved)
	}
}

// A turn-capped cell is a no-result limit of that cell, not of the provider:
// the run goes on, and the cell gives no check result.
func TestRunGoesOnAfterTurnCap(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"claude/opus/high", "codex/sol/high"})
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		f.calls = append(f.calls, "cell:"+c.Entry)
		if strings.HasPrefix(c.Entry, "claude/") {
			return CellResult{State: CellFailedLimit, Reason: "turn cap", Tokens: 10}, nil
		}
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate}, nil
	})
	o := f.execute(t)
	if !o.Done || o.Stopped != "" {
		t.Fatalf("outcome %+v, want done", o)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls %v, want both cells", f.calls)
	}
	j, err := journal.Open(f.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	saved := f.saved(t)
	if err := RecordResults(j, testConfig(t), saved, Manifest{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cs, err := journal.ReadAll[journal.CalibrationResult](j, journal.CalibrationFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Entry != "codex/sol/high" {
		t.Fatalf("results %+v, want the codex cell's only", cs)
	}
}

// A check_setup_changed cell ran to its end with the files it changed, but
// gives no check result and no pair to judge: the person decides.
func TestRunKeepsCheckSetupChanged(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"claude/opus/high", "codex/sol/high"})
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		if strings.HasPrefix(c.Entry, "claude/") {
			return CellResult{State: CellSetupChanged, Passed: true, SetupChanged: []string{"Makefile"}, Tokens: runEstimate}, nil
		}
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate}, nil
	})
	if o := f.execute(t); !o.Done {
		t.Fatalf("outcome %+v, want done", o)
	}
	saved := f.saved(t)
	c := saved.Cells[0]
	if c.State != CellSetupChanged || !slices.Equal(c.SetupChanged, []string{"Makefile"}) || !c.Ran() {
		t.Fatalf("cell %+v, want check_setup_changed for Makefile, ran", c)
	}
	if !strings.Contains(f.out.String(), "check setup changed: Makefile") {
		t.Fatalf("output lacks the changed setup:\n%s", f.out.String())
	}
	if pairs := Pairs(saved); len(pairs) != 0 {
		t.Fatalf("pairs %+v, want none", pairs)
	}
	j, err := journal.Open(f.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordResults(j, testConfig(t), saved, Manifest{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cs, err := journal.ReadAll[journal.CalibrationResult](j, journal.CalibrationFile)
	if err != nil || len(cs) != 1 || cs[0].Entry != "codex/sol/high" {
		t.Fatalf("results %+v, %v; want the codex cell's only", cs, err)
	}
	var status bytes.Buffer
	if err := WriteStatus(&status, saved, f.runner.Plan, testTime); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.String(), "check_setup_changed 1") {
		t.Fatalf("status lacks the cell:\n%s", status.String())
	}
}

func TestRunWindowPause(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium", "codex/sol/high"})
	f.quota.window = 60
	f.run.Spend.Windows = map[string]WindowSpend{"codex": {ResetsAt: f.quota.reset, StartUsed: 12, LastUsed: 60}}
	o := f.execute(t)
	if !o.Pause.Equal(f.quota.reset) || o.Done || o.Stopped != "" {
		t.Fatalf("outcome %+v, want a pause until %s", o, f.quota.reset)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v, want none", f.calls)
	}
	if saved := f.saved(t); !saved.Pause.Equal(f.quota.reset) {
		t.Fatalf("run.json Pause = %s", saved.Pause)
	}
	want := "paused until " + f.quota.reset.UTC().Format(time.RFC3339) + "; resume with duolog calibrate resume"
	if !strings.Contains(f.out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, f.out.String())
	}
}

func TestRunWindowShareCountsEstimate(t *testing.T) {
	f := newRunFixture(t, []string{"t1", "t2", "t3", "t4", "t5", "t6"}, []string{"codex/sol/medium"})
	// The provider shows the cells' spend late: the five-hour reading never
	// rises, so only the estimate of 5 points per cell counts. A cap of 20%
	// leaves room for 4 cells.
	f.runner.Cfg.Calibration.MaxWindowShare = 0.2
	o := f.execute(t)
	if !o.Pause.Equal(f.quota.reset) || o.Done {
		t.Fatalf("outcome %+v, want a pause until %s:\n%s", o, f.quota.reset, f.out.String())
	}
	if len(f.calls) != 4 {
		t.Fatalf("calls %v, want 4 cells before the pause", f.calls)
	}
	if own := f.saved(t).Spend.Own("codex", testTime); own != 4*BoundWindow {
		t.Fatalf("own five-hour share %v, want %v", own, 4*BoundWindow)
	}
}

func TestRunWindowShareCountsLargerRise(t *testing.T) {
	f := newRunFixture(t, []string{"t1", "t2"}, []string{"codex/sol/medium"})
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		f.calls = append(f.calls, "cell:"+task.ID)
		f.quota.window += 8
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate, Seconds: 60}, nil
	})
	f.execute(t)
	// The measured 16, and the second cell's charge, which no reading after
	// that cell has shown yet.
	if own := f.saved(t).Spend.Own("codex", testTime); own != 16+BoundWindow {
		t.Fatalf("own five-hour share %v, want %v", own, 16+BoundWindow)
	}
}

// nTasks is t1 to tn.
func nTasks(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("t%d", i+1)
	}
	return ids
}

func TestRunStopsWhenRefreshFails(t *testing.T) {
	f := newRunFixture(t, nTasks(3), []string{"codex/sol/medium"})
	// The admission reading succeeds; the fresh reading after the cell fails.
	f.quota.failAt = 2
	o := f.execute(t)
	if o.Stopped != "quota unavailable" || o.Done {
		t.Fatalf("outcome %+v, want stopped: quota unavailable:\n%s", o, f.out.String())
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls %v, want the one cell before the failed reading", f.calls)
	}
	saved := f.saved(t)
	if saved.Stopped != "quota unavailable" || saved.Cells[0].State != CellDone || !saved.Cells[0].Passed {
		t.Fatalf("run.json %+v, want the first cell done and the stop saved", saved)
	}
	if got := saved.Spend.WeeklyPct["codex"]; got != BoundWeekly {
		t.Fatalf("spend codex = %v, want the cell's estimate %v", got, BoundWeekly)
	}
}

func TestRunChargesFailedRewrite(t *testing.T) {
	f := newRunFixture(t, nTasks(1), []string{"codex/sol/medium"})
	f.needsRewrite(t, "t1")
	f.rewrite(func(task TaskRecord) (string, int64, error) {
		return "", 4000, errors.New("worker crashed")
	})
	if _, err := f.runner.Execute(context.Background()); err == nil {
		t.Fatal("Execute = nil, want the rewrite error")
	}
	// Without a rate a rewrite counts the bound scaled by its tokens over a
	// cell's of its class.
	if got, want := f.saved(t).Spend.WeeklyPct["claude"], JudgeCost(nil, runEstimate).WeeklyPct; got != want {
		t.Fatalf("spend claude = %v after a failed rewrite, want %v", got, want)
	}
}

func TestRunChargesAttemptBeforeWorkerStarts(t *testing.T) {
	// A crash during the cell must not lose the attempt: run.json holds its
	// charge while the worker runs.
	f := newRunFixture(t, nTasks(1), []string{"codex/sol/medium"})
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		f.calls = append(f.calls, "cell:"+task.ID)
		saved := f.saved(t)
		if saved.Cells[0].State != CellRunning || saved.Spend.WeeklyPct["codex"] != BoundWeekly {
			t.Errorf("run.json while the cell runs: %+v, want running with %v charged", saved, BoundWeekly)
		}
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate}, nil
	})
	f.execute(t)
	if got := f.saved(t).Spend.WeeklyPct["codex"]; got != BoundWeekly {
		t.Fatalf("spend codex = %v, want one cell's %v", got, BoundWeekly)
	}
}

func TestRunReserveCountsLaggingFiveHour(t *testing.T) {
	// The five-hour reading stays at 80 while each cell spends the bound of
	// 5: two cells leave the lead its 10%, a third would not.
	f := newRunFixture(t, nTasks(6), []string{"codex/sol/medium"})
	f.quota.window = 80
	o := f.execute(t)
	if o.Stopped != "lead reserve" {
		t.Fatalf("outcome %+v, want stopped at the lead reserve:\n%s", o, f.out.String())
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls %v, want 2 cells", f.calls)
	}
}

func TestRunReserveCountsLaggingWeekly(t *testing.T) {
	// Each cell's weekly spend of 1 shows one reading late. The run counts it
	// once: from 80, five cells leave the lead its 15%, a sixth would not.
	f := newRunFixture(t, nTasks(7), []string{"codex/sol/medium"})
	f.quota.weekly = 80
	late := 0.0
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		f.calls = append(f.calls, "cell:"+task.ID)
		f.quota.weekly += late
		late = 1
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate}, nil
	})
	o := f.execute(t)
	if o.Stopped != "lead reserve" {
		t.Fatalf("outcome %+v, want stopped at the lead reserve:\n%s", o, f.out.String())
	}
	if len(f.calls) != 5 {
		t.Fatalf("calls %v, want 5 cells", f.calls)
	}
}

// The lead spends while a cell's own spend does not show yet: the reading
// rises by the lead's share alone. The cell's charge stays outstanding on top
// of that reading, so a second cell, which would leave the lead less than its
// reserve, is refused.
func TestRunReserveCountsLeadSpendDuringLaggingCell(t *testing.T) {
	for _, tc := range []struct {
		name string
		lead float64
	}{{"five-hour", 5}, {"weekly", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRunFixture(t, nTasks(3), []string{"codex/sol/medium"})
			at := &f.quota.window
			f.quota.window = 80
			if tc.name == "weekly" {
				at = &f.quota.weekly
				f.quota.weekly = 83
			}
			f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
				f.calls = append(f.calls, "cell:"+task.ID)
				*at += tc.lead
				return CellResult{State: CellDone, Passed: true, Tokens: runEstimate}, nil
			})
			o := f.execute(t)
			if o.Stopped != "lead reserve" || len(f.calls) != 1 {
				t.Fatalf("outcome %+v after %d cells, want the lead reserve after 1:\n%s", o, len(f.calls), f.out.String())
			}
		})
	}
}

// Once a reading after the cell shows its own spend, the charge is no longer
// outstanding, so it counts once.
func TestRunChargeClearsWhenReadingCatchesUp(t *testing.T) {
	f := newRunFixture(t, nTasks(1), []string{"codex/sol/medium"})
	f.quota.window = 30
	f.execute(t)
	spend := f.saved(t).Spend
	level := func() float64 {
		t.Helper()
		ws, err := f.runner.Quota.Get(context.Background(), "codex", true)
		if err != nil {
			t.Fatal(err)
		}
		ws = spend.WithResets("codex", ws, testTime)
		spend.Track("codex", ws)
		five := accountWindow(ws, KindFiveHour)
		return five.UsedPercent + spend.OutstandingFor("codex", *five)
	}
	if got := level(); got != 30+BoundWindow {
		t.Fatalf("five-hour level %v before the reading shows the cell, want %v", got, 30+BoundWindow)
	}
	f.quota.window = 30 + BoundWindow
	if got := level(); got != 30+BoundWindow {
		t.Fatalf("five-hour level %v once the reading shows the cell, want %v counted once", got, 30+BoundWindow)
	}
}

// The plan keeps a share of the cap for judging: cells stop at the cap less
// the judge share still to spend, so the pairs can still be judged.
func TestRunCellsKeepJudgeShare(t *testing.T) {
	f := newRunFixture(t, nTasks(12), []string{"codex/sol/medium"})
	f.runner.Plan.JudgeShare = map[string]float64{"codex": 3}
	o := f.execute(t)
	if o.Stopped != "approved cap" || len(f.calls) != 7 {
		t.Fatalf("outcome %+v after %d cells, want the approved cap after 7:\n%s", o, len(f.calls), f.out.String())
	}
}

// A rewrite that costs more than its estimate is charged to the cap, not to
// the judge share: the judging part of the share stays held, so the cells
// stop one sooner. Here the share is 2 points of judging and one rewrite,
// which costs 1 point more than estimated.
func TestRunRewriteOverrunKeepsJudgeShare(t *testing.T) {
	f := newRunFixture(t, nTasks(12), []string{"claude/opus/high"})
	rewrite := JudgeCost(nil, runEstimate).WeeklyPct
	f.runner.Plan.JudgeShare = map[string]float64{"claude": 2 + rewrite}
	f.needsRewrite(t, "t1")
	f.rewrite(func(task TaskRecord) (string, int64, error) {
		f.calls = append(f.calls, "rewrite:"+task.ID)
		f.quota.weekly += rewrite + 1
		return "Add a function Parse for " + task.ID + ".", 5000, nil
	})
	o := f.execute(t)
	if o.Stopped != "approved cap" || len(f.calls) != 7 {
		t.Fatalf("outcome %+v after %v, want the approved cap after the rewrite and 6 cells:\n%s", o, f.calls, f.out.String())
	}
	saved := f.saved(t)
	if got := saved.Spend.WeeklyPct["claude"]; !near(got, rewrite+1+6*BoundWeekly) {
		t.Fatalf("weekly spend %v, want the rewrite's rise and 6 cells", got)
	}
	if got := saved.Spend.JudgePct["claude"]; !near(got, rewrite) {
		t.Fatalf("judge spend %v, want only the rewrite's estimate %v", got, rewrite)
	}
}

// A rewrite of a reserve task, after the planned task's statement was
// refused, is charged to the cap, not to the judge share.
func TestRunRewriteRetryKeepsJudgeShare(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"claude/opus/high"})
	f.addTask(t, &f.runner.Manifest, "t9")
	f.runner.Manifest.Reserve["implement"] = []string{"t9"}
	f.needsRewrite(t, "t1", "t9")
	rewrite := JudgeCost(nil, runEstimate).WeeklyPct
	f.runner.Plan.JudgeShare = map[string]float64{"claude": 2 + rewrite}
	f.rewrite(func(task TaskRecord) (string, int64, error) {
		f.calls = append(f.calls, "rewrite:"+task.ID)
		if task.ID == "t1" {
			return "Add Parse as in abcdef0123456789.", 5000, nil
		}
		return "Add a function Parse for " + task.ID + ".", 5000, nil
	})
	if o := f.execute(t); !o.Done {
		t.Fatalf("outcome %+v, want done:\n%s", o, f.out.String())
	}
	if want := []string{"rewrite:t1", "rewrite:t9", "cell:t9"}; !slices.Equal(f.calls, want) {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	saved := f.saved(t)
	if got := saved.Spend.WeeklyPct["claude"]; !near(got, 2*rewrite+BoundWeekly) {
		t.Fatalf("weekly spend %v, want both rewrites and the cell", got)
	}
	if got := saved.Spend.JudgePct["claude"]; !near(got, rewrite) {
		t.Fatalf("judge spend %v, want only the planned rewrite's estimate %v", got, rewrite)
	}
}

// Cells that cost more than planned leave no room for the judge share: the
// run stops for a new approval instead of spending it on cells.
func TestRunReapprovesWhenJudgeShareNoLongerFits(t *testing.T) {
	f := newRunFixture(t, nTasks(3), []string{"codex/sol/medium"})
	f.runner.Plan.JudgeShare = map[string]float64{"codex": 3}
	f.run.Spend.WeeklyPct = map[string]float64{"codex": 8}
	if err := f.run.Save(f.stateDir); err != nil {
		t.Fatal(err)
	}
	o := f.execute(t)
	if !o.Reapprove || o.Stopped != "re-approve" || len(f.calls) != 0 {
		t.Fatalf("outcome %+v after %d cells, want a stop for a new approval before any cell:\n%s", o, len(f.calls), f.out.String())
	}
	if !strings.Contains(f.out.String(), "plan again: duolog calibrate plan") {
		t.Fatalf("output lacks plan again:\n%s", f.out.String())
	}
}

// A cap stop for one agent skips only that agent's cells; the other agent's
// cells still run, and the run part ends when none are left.
func TestRunCapStopSkipsOnlyCappedAgent(t *testing.T) {
	f := newRunFixture(t, nTasks(3), []string{"codex/sol/medium", "claude/opus/high"})
	f.run.Spend.WeeklyPct = map[string]float64{"codex": 9.5}
	if err := f.run.Save(f.stateDir); err != nil {
		t.Fatal(err)
	}
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		f.calls = append(f.calls, c.Entry)
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate}, nil
	})
	o := f.execute(t)
	if want := []string{"claude/opus/high", "claude/opus/high", "claude/opus/high"}; !slices.Equal(f.calls, want) {
		t.Fatalf("calls %v, want the 3 claude cells:\n%s", f.calls, f.out.String())
	}
	if o.Stopped != "approved cap" {
		t.Fatalf("outcome %+v, want the run part ended at the approved cap", o)
	}
	saved := f.saved(t)
	for _, c := range saved.Cells {
		if entryAgent(c.Entry) == "codex" && (c.State != CellSkipped || c.Reason != "cut to cap") {
			t.Fatalf("codex cell %+v, want skipped, cut to cap", c)
		}
	}
	if saved.Step != "run" || !strings.Contains(f.out.String(), "next: duolog calibrate judge") {
		t.Fatalf("step %q, output:\n%s\nwant the judge step next", saved.Step, f.out.String())
	}
}

// After the pilot the plan is estimated again with the cells that ran and
// the run's spend, so what the run spent counts toward the cap and the cells
// that no longer fit are cut.
func TestRunCutAfterPilotCountsSpent(t *testing.T) {
	f := newRunFixture(t, nTasks(6), []string{"codex/sol/medium", "codex/sol/high"})
	if err := WriteRates(f.stateDir, map[string]Rate{"claude": {WeeklyPerMTok: 0.01, WindowPerMTok: 0.01}}); err != nil {
		t.Fatal(err)
	}
	f.runner.Plan.Pilot = true
	f.step = 0.5
	f.run.Spend.WeeklyPct = map[string]float64{"codex": 5}
	if err := f.run.Save(f.stateDir); err != nil {
		t.Fatal(err)
	}
	f.execute(t)
	if len(f.calls) != pilotCells {
		t.Fatalf("calls %v, want only the pilot cells:\n%s", f.calls, f.out.String())
	}
	for _, c := range f.saved(t).Cells[pilotCells:] {
		if c.State != CellSkipped || c.Reason != "cut to cap" {
			t.Fatalf("cell %+v, want skipped, cut to cap", c)
		}
	}
}

// A cell Codex refuses before its worker starts costs nothing: the run
// readies and checks the real tree first and charges only an attempt it
// admitted. Once the person trusts the tree, the cell runs and is charged
// once.
func TestRunTrustRefusalChargesNothing(t *testing.T) {
	f := newCellFixture(t)
	trustCodex(t, f.home, f.slot, false, false)
	workers := 0
	work := worked(t)
	f.env.Delegate = func(ctx context.Context, cfg *config.Config, s *store.Store, role, taskPath, thread string) (delegate.Outcome, error) {
		workers++
		return work(ctx, cfg, s, role, taskPath, thread)
	}
	run := &Run{ID: "cal-1", Step: "plan", Created: testTime, Approved: &Approval{Hash: "h", Time: testTime},
		Cells: []Cell{{ID: "c001", Task: f.task.ID, Class: "implement", Entry: "codex/sol/high", Round: 1, State: CellPending}}}
	if err := run.Save(f.env.StateDir); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(f.runDir)
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}
	var out bytes.Buffer
	runner := func(run *Run) *Runner {
		return &Runner{StateDir: f.env.StateDir, Root: f.root, Cfg: f.env.Cfg, Run: run, Plan: Plan{Run: run.ID, CapWeekly: 10, CapWindow: 50}, Manifest: m,
			Quota: &QuotaCache{Read: q.read, Now: func() time.Time { return testTime }}, Now: func() time.Time { return testTime },
			Cell: func(ctx context.Context, c Cell, t TaskRecord, start StartFunc) (CellResult, error) {
				return RunCell(ctx, f.env, c, t, start)
			},
			Rewrite: func(context.Context, TaskRecord, StartFunc) (string, int64, error) {
				return "", 0, errors.New("no rewrite")
			},
			Out: &out}
	}
	for range 2 {
		if _, err := runner(run).Execute(context.Background()); err == nil || !strings.Contains(err.Error(), f.slot) {
			t.Fatalf("Execute on an untrusted tree = %v, want a refusal naming %s", err, f.slot)
		}
		saved, err := LoadRun(f.env.StateDir, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := saved.Spend.WeeklyPct["codex"]; got != 0 || workers != 0 {
			t.Fatalf("after a trust refusal: spend %v, %d workers; want nothing charged and no worker", got, workers)
		}
		if saved.Cells[0].State != CellPending {
			t.Fatalf("cell %+v after a trust refusal, want pending", saved.Cells[0])
		}
		run = saved
	}

	trustCodex(t, f.home, f.slot, true, true)
	o, err := runner(run).Resume(context.Background())
	if err != nil || !o.Done {
		t.Fatalf("Resume = %+v, %v; want done:\n%s", o, err, out.String())
	}
	saved, err := LoadRun(f.env.StateDir, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Spend.WeeklyPct["codex"]; got != BoundWeekly || workers != 1 || saved.Cells[0].State != CellDone {
		t.Fatalf("after trusting: spend %v, %d workers, cell %+v; want one attempt charged and done", got, workers, saved.Cells[0])
	}
}

// A cell the delegation refuses after admission but before its worker
// starts, here for a lead that is not registered, costs nothing: the charge
// is released, the cell is pending again and no worker ran.
func TestRunDelegateRefusalReleasesCharge(t *testing.T) {
	f := newCellFixture(t)
	var out delegate.Outcome
	fake := f.realClaude(t, testbin.Scenario{Stdout: parseAnswer}, &out)
	f.env.Lead = "lead-gone"
	run := &Run{ID: "cal-1", Step: "plan", Created: testTime, Approved: &Approval{Hash: "h", Time: testTime},
		Cells: []Cell{{ID: "c001", Task: f.task.ID, Class: "implement", Entry: f.cell.Entry, Round: 1, State: CellPending}}}
	if err := run.Save(f.env.StateDir); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(f.runDir)
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}
	r := &Runner{StateDir: f.env.StateDir, Root: f.root, Cfg: f.env.Cfg, Run: run, Plan: Plan{Run: run.ID, CapWeekly: 10, CapWindow: 50}, Manifest: m,
		Quota: &QuotaCache{Read: q.read, Now: func() time.Time { return testTime }}, Now: func() time.Time { return testTime },
		Cell: func(ctx context.Context, c Cell, t TaskRecord, start StartFunc) (CellResult, error) {
			return RunCell(ctx, f.env, c, t, start)
		},
		Rewrite: func(context.Context, TaskRecord, StartFunc) (string, int64, error) {
			return "", 0, errors.New("no rewrite")
		},
		Out: &bytes.Buffer{}}
	if _, err := r.Execute(context.Background()); err == nil || !strings.Contains(err.Error(), "lead-gone") {
		t.Fatalf("Execute = %v, want the unknown lead refused", err)
	}
	if _, err := os.Stat(fake + ".args.json"); !os.IsNotExist(err) {
		t.Fatalf("a worker ran for a refused delegation: %v", err)
	}
	saved, err := LoadRun(f.env.StateDir, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Spend.WeeklyPct["claude"]; got != 0 {
		t.Fatalf("claude weekly spend %v after a refusal before the worker, want 0", got)
	}
	for key, o := range saved.Spend.Outstanding {
		if len(o.Charges) > 0 {
			t.Fatalf("window %s keeps charges %+v after a refusal before the worker", key, o.Charges)
		}
	}
	if c := saved.Cells[0]; c.State != CellPending || c.Tree != "" {
		t.Fatalf("cell %+v after a refusal before the worker, want pending without a tree", c)
	}
}

// A rewrite the delegation refuses before its worker starts is not charged.
func TestRunRewriteNotStartedReleasesCharge(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"claude/opus/high"})
	f.needsRewrite(t, "t1")
	f.rewrite(func(task TaskRecord) (string, int64, error) {
		return "", 0, &delegate.NotStartedError{Err: errors.New("lead lead-1 is not registered")}
	})
	if _, err := f.runner.Execute(context.Background()); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Execute = %v, want the refusal", err)
	}
	saved := f.saved(t)
	if saved.Spend.WeeklyPct["claude"] != 0 || saved.Spend.JudgePct["claude"] != 0 {
		t.Fatalf("spend %+v after a rewrite that never started, want none", saved.Spend)
	}
	for key, o := range saved.Spend.Outstanding {
		if len(o.Charges) > 0 {
			t.Fatalf("window %s keeps charges %+v", key, o.Charges)
		}
	}
}

func TestRunWindowWithoutResetCountsShare(t *testing.T) {
	// A five-hour window without a reset time counts from the run's first
	// reading and pauses for five hours.
	f := newRunFixture(t, nTasks(6), []string{"codex/sol/medium"})
	f.quota.noReset = true
	f.runner.Cfg.Calibration.MaxWindowShare = 0.2
	o := f.execute(t)
	if want := testTime.Add(5 * time.Hour); !o.Pause.Equal(want) {
		t.Fatalf("outcome %+v, want a pause until %s:\n%s", o, want, f.out.String())
	}
	if len(f.calls) != 4 {
		t.Fatalf("calls %v, want 4 cells before the pause", f.calls)
	}
}

func TestRunModelLimitKeepsReserve(t *testing.T) {
	// The account windows are low, but sol's own weekly limit is at 85: a sol
	// cell would cross the lead reserve there; a terra cell is not limited by it.
	f := newRunFixture(t, nTasks(1), []string{"codex/terra/medium", "codex/sol/medium"})
	f.quota.models = []Window{{Kind: KindWeekly, Model: "sol", UsedPercent: 85, ResetsAt: testTime.Add(72 * time.Hour)}}
	o := f.execute(t)
	if o.Stopped != "lead reserve" {
		t.Fatalf("outcome %+v, want stopped at the lead reserve:\n%s", o, f.out.String())
	}
	saved := f.saved(t)
	if saved.Cells[0].State != CellDone || saved.Cells[1].State == CellDone {
		t.Fatalf("cells %+v, want the terra cell done and the sol cell not run", saved.Cells)
	}
}

func TestRunUsesApprovedCaps(t *testing.T) {
	// The plan was approved with 5% weekly and 20% of a five-hour window; the
	// config now says 10% and 50%. The run keeps to the approval.
	f := newRunFixture(t, nTasks(7), []string{"codex/sol/medium"})
	f.runner.Plan.CapWeekly, f.runner.Plan.CapWindow = 5, 20
	o := f.execute(t)
	if !o.Pause.Equal(f.quota.reset) || len(f.calls) != 4 {
		t.Fatalf("outcome %+v after %d cells, want a pause after 4:\n%s", o, len(f.calls), f.out.String())
	}
	f.runner.Plan.CapWindow = 50
	f.runner.Now = func() time.Time { return f.quota.reset.Add(time.Minute) }
	f.quota.reset = f.quota.reset.Add(5 * time.Hour)
	o = f.execute(t)
	if o.Stopped != "approved cap" || len(f.calls) != 5 {
		t.Fatalf("outcome %+v after %d cells, want the approved cap after 5:\n%s", o, len(f.calls), f.out.String())
	}
}

func TestApprovedLimits(t *testing.T) {
	cfg := testConfig(t)
	p := Plan{CapWeekly: 5, CapWindow: 50, CellMinutes: 30, CellTurns: 100}
	cfg.Calibration.MaxWindowShare, cfg.Calibration.CellMaxTurns = 0.3, 40
	got := p.Limit(cfg).Calibration
	if got.MaxWeeklyShare != 0.05 || got.MaxWindowShare != 0.3 || got.CellMaxMinutes != 30 || got.CellMaxTurns != 40 {
		t.Fatalf("limits %+v, want the stricter of plan and config", got)
	}
	if cfg.Calibration.MaxWeeklyShare != 0.10 {
		t.Fatalf("Limit changed its input: %+v", cfg.Calibration)
	}
}

func TestRunRechecksApprovedChecks(t *testing.T) {
	f := newRunFixture(t, nTasks(1), []string{"codex/sol/medium"})
	f.runner.Plan.Checks = map[string][]string{"implement": {"make check-everything"}}
	_, err := f.runner.Execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checks of class implement changed since approval; plan again") {
		t.Fatalf("Execute = %v, want the checks changed since approval", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v, want none", f.calls)
	}
}

func TestRunApprovedCapEndsRunPart(t *testing.T) {
	// Without a measured rate each cell counts 1% weekly: 10 cells reach the
	// cap of 10%, the other 2 are cut and the run goes on to judging.
	f := newRunFixture(t, nTasks(12), []string{"codex/sol/medium"})
	o := f.execute(t)
	if o.Stopped != "approved cap" || len(f.calls) != 10 {
		t.Fatalf("outcome %+v after %d cells, want the approved cap after 10:\n%s", o, len(f.calls), f.out.String())
	}
	saved := f.saved(t)
	if saved.Step != "run" {
		t.Fatalf("Step = %q, want run", saved.Step)
	}
	for _, c := range saved.Cells[10:] {
		if c.State != CellSkipped || c.Reason != "cut to cap" {
			t.Fatalf("cell %+v, want skipped, cut to cap", c)
		}
	}
	if !strings.Contains(f.out.String(), "next: duolog calibrate judge") {
		t.Fatalf("output lacks next: duolog calibrate judge:\n%s", f.out.String())
	}
	var b bytes.Buffer
	if err := WriteStatus(&b, saved, f.runner.Plan, testTime); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "next: duolog calibrate judge") {
		t.Fatalf("status lacks next: duolog calibrate judge:\n%s", b.String())
	}
}

func TestValidStatement(t *testing.T) {
	rec := TaskRecord{Subject: "add the widget parser", Commits: []string{"abcdef0123456789abcdef0123456789abcdef01"}, HiddenNames: []string{"Parse"}}
	names := []string{"Robin Sample"}
	if err := ValidStatement("Add a function Parse that splits a line on commas.", rec, names); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"Add Parse as abcdef0 did.",
		"Add the widget parser: Add the Widget Parser with Parse.",
		"Add Parse:\n\n```go\nfunc Parse() {}\n```\n",
		"Add a function that splits a line on commas.",
		"Add a function Parser that splits a line on commas.",
		"Add Parse, as robin sample asked.",
		"Add Parse; ask robin@example.invalid.",
		"Add Parse.\n\nCo-Authored-By: Casey",
		"Add Parse within a budget of 3.50 USD.",
		"",
	} {
		if err := ValidStatement(bad, rec, names); err == nil {
			t.Fatalf("ValidStatement(%q) = nil, want a refusal", bad)
		}
	}
}

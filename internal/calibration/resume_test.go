package calibration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
)

func TestResumeSkipsDoneRerunsRunning(t *testing.T) {
	f := newRunFixture(t, []string{"t1", "t2", "t3"}, []string{"codex/sol/medium"})
	stale := filepath.Join(t.TempDir(), "tree")
	write(t, stale, "a.txt", "a\n")
	f.run.Cells[0].State, f.run.Cells[0].Passed = CellDone, true
	f.run.Cells[1].State, f.run.Cells[1].Tree = CellRunning, stale
	f.run.Stopped = "lead reserve"
	if err := f.run.Save(f.stateDir); err != nil {
		t.Fatal(err)
	}
	o, err := f.runner.Resume(context.Background())
	if err != nil {
		t.Fatal(err, f.out.String())
	}
	if !o.Done {
		t.Fatalf("outcome %+v, want done:\n%s", o, f.out.String())
	}
	if want := []string{"cell:t2", "cell:t3"}; !slices.Equal(f.calls, want) {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	if exists(stale) {
		t.Fatalf("stale tree %s is still there", stale)
	}
	saved := f.saved(t)
	if saved.Stopped != "" {
		t.Fatalf("run.json Stopped = %q, want none", saved.Stopped)
	}
	for _, c := range saved.Cells {
		if c.State != CellDone || c.Tree != "" {
			t.Fatalf("cell %+v, want done without a tree", c)
		}
	}
}

// Resume empties a running cell's tree but keeps the Codex hooks the person
// approved there, so the rerun is not refused again.
func TestResumeKeepsApprovedHooks(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	stale := filepath.Join(t.TempDir(), "tree")
	write(t, stale, "a.txt", "a\n")
	write(t, stale, ".codex/hooks.json", gateHooks)
	f.run.Cells[0].State, f.run.Cells[0].Tree = CellRunning, stale
	if err := f.run.Save(f.stateDir); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.Resume(context.Background()); err != nil {
		t.Fatal(err, f.out.String())
	}
	onlyHooks(t, stale)
}

// A running cell's tree that is a link is removed as a link; what it points
// at stays as it is.
func TestResumeRemovesLinkedTree(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	target := t.TempDir()
	write(t, target, "keep.txt", "k\n")
	link := filepath.Join(t.TempDir(), "tree")
	symlink(t, filepath.Dir(link), "tree", target)
	f.run.Cells[0].State, f.run.Cells[0].Tree = CellRunning, link
	if err := f.run.Save(f.stateDir); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.Resume(context.Background()); err != nil {
		t.Fatal(err, f.out.String())
	}
	if exists(link) {
		t.Fatalf("link %s is still there", link)
	}
	if !exists(filepath.Join(target, "keep.txt")) {
		t.Fatalf("removing the tree emptied the folder %s its link points at", target)
	}
}

func TestResumeKeepsApprovedTotal(t *testing.T) {
	f := newRunFixture(t, []string{"t1", "t2"}, []string{"codex/sol/medium"})
	f.run.Spend.WeeklyPct = map[string]float64{"codex": 9.0}
	// The weekly window reset since the run stopped: its reading is low again.
	f.quota.weekly = 2
	if err := f.run.Save(f.stateDir); err != nil {
		t.Fatal(err)
	}
	o, err := f.runner.Resume(context.Background())
	if err != nil {
		t.Fatal(err, f.out.String())
	}
	if o.Stopped != "approved cap" {
		t.Fatalf("outcome %+v, want stopped at approved cap:\n%s", o, f.out.String())
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls %v, want one cell", f.calls)
	}
	if got := f.saved(t).Spend.WeeklyPct["codex"]; got != 10 {
		t.Fatalf("spend codex = %v, want 10", got)
	}
}

func TestResumeRechecksRepository(t *testing.T) {
	ctx := context.Background()
	root := project(t)
	write(t, root, "a.txt", "a\n")
	sha := commit(t, root, "add a")
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	f.runner.Root = root
	f.runner.Manifest.Tasks[0].Sources = []string{"commit " + sha}
	if err := Recheck(ctx, root, f.runner.Cfg, f.run, f.runner.Manifest, f.runner.Plan); err != nil {
		t.Fatalf("Recheck = %v, want nil while the commit exists", err)
	}

	gone := strings.Repeat("e", 40)
	f.runner.Manifest.Tasks[0].Sources = []string{"commit " + sha, "commit " + gone}
	_, err := f.runner.Resume(ctx)
	if err == nil || !strings.Contains(err.Error(), "commit "+gone+" of task t1 is gone; plan again") {
		t.Fatalf("Resume = %v, want the commit is gone; plan again", err)
	}

	f.runner.Manifest.Tasks[0].Sources = []string{"commit " + sha}
	f.runner.Plan.Checks = map[string][]string{"implement": {"make check-everything"}}
	_, err = f.runner.Resume(ctx)
	if err == nil || !strings.Contains(err.Error(), "checks of class implement changed since approval; plan again") {
		t.Fatalf("Resume = %v, want the checks changed since approval", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v, want none", f.calls)
	}
}

// The approved plan records the judges and their rubrics: changing either
// afterwards needs a new approval before the run goes on.
func TestResumeRefusesChangedJudges(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(cfg *config.Config)
	}{
		{"judge order", "judges changed since approval; plan again", func(cfg *config.Config) {
			slices.Reverse(cfg.Learning.Judges)
		}},
		{"judge model", "judges changed since approval; plan again", func(cfg *config.Config) {
			cfg.Learning.Judges[0].Effort = "medium"
		}},
		{"rubric", "judge rubrics changed since approval; plan again", func(cfg *config.Config) {
			class := cfg.Learning.Classes["implement"]
			class.Rubric = append(slices.Clone(class.Rubric), "kept the style")
			cfg.Learning.Classes["implement"] = class
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRunFixture(t, nTasks(1), []string{"codex/sol/medium"})
			f.runner.Plan = buildPlan(t, PlanInput{Cfg: f.runner.Cfg, Manifest: f.runner.Manifest, Rates: smallRates(), RunID: f.run.ID})
			cfg := *f.runner.Cfg
			cfg.Learning.Judges = slices.Clone(cfg.Learning.Judges)
			cfg.Learning.Classes = maps.Clone(cfg.Learning.Classes)
			tc.change(&cfg)
			f.runner.Cfg = &cfg
			_, err := f.runner.Resume(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Resume = %v, want %q", err, tc.want)
			}
			if len(f.calls) != 0 {
				t.Fatalf("calls %v, want none", f.calls)
			}
		})
	}
}

func TestResumeRefreshesQuota(t *testing.T) {
	ctx := context.Background()
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	// A measured rate of 2 points per 1M tokens makes a cell cost 2 points.
	if err := WriteRates(f.stateDir, map[string]Rate{"codex": {WeeklyPerMTok: 2}}); err != nil {
		t.Fatal(err)
	}
	now := testTime
	f.runner.Quota.Now = func() time.Time { return now }
	if _, err := f.runner.Quota.Get(ctx, "codex", false); err != nil {
		t.Fatal(err)
	}
	reads := f.quota.reads
	now = testTime.Add(10 * time.Second)
	// The lead worked since: a fresh reading leaves no room above the reserve.
	f.quota.weekly = 84
	o, err := f.runner.Resume(ctx)
	if err != nil {
		t.Fatal(err, f.out.String())
	}
	if f.quota.reads == reads {
		t.Fatal("Resume used the cached reading without calling Read")
	}
	if o.Stopped != "lead reserve" || len(f.calls) != 0 {
		t.Fatalf("outcome %+v, calls %v; want stopped at lead reserve with no cell", o, f.calls)
	}
}

func TestResumeBeforePauseWaits(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	pause := testTime.Add(time.Hour)
	f.run.Pause = pause
	if err := f.run.Save(f.stateDir); err != nil {
		t.Fatal(err)
	}
	o, err := f.runner.Resume(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !o.Pause.Equal(pause) || o.Done || o.Stopped != "" {
		t.Fatalf("outcome %+v, want the pause until %s", o, pause)
	}
	if len(f.calls) != 0 || f.quota.reads != 0 {
		t.Fatalf("calls %v, %d quota reads; want none", f.calls, f.quota.reads)
	}
	if saved := f.saved(t); !saved.Pause.Equal(pause) {
		t.Fatalf("run.json Pause = %s, want %s", saved.Pause, pause)
	}
}

func TestInterruptChargesAttemptOnce(t *testing.T) {
	// cancel ctx inside the cell: State running, the attempt charged at its
	// estimate, tree removed; Resume: the cell runs once more and Spend grows
	// by one more cell, not by the interrupted one again
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	tree := filepath.Join(t.TempDir(), "tree")
	f.runner.Tree = tree
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interrupt := true
	f.cell(func(c Cell, task TaskRecord) (CellResult, error) {
		f.calls = append(f.calls, "cell:"+task.ID)
		write(t, tree, "work.txt", "half done\n")
		if interrupt {
			cancel()
			return CellResult{Tokens: runEstimate / 2}, ctx.Err()
		}
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate, Seconds: 60}, nil
	})
	if _, err := f.runner.Execute(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute = %v, want context canceled", err)
	}
	saved := f.saved(t)
	if c := saved.Cells[0]; c.State != CellRunning {
		t.Fatalf("cell %+v, want running", c)
	}
	if got := saved.Spend.WeeklyPct["codex"]; got != BoundWeekly {
		t.Fatalf("spend codex = %v after an interrupt, want the attempt's %v", got, BoundWeekly)
	}
	if own := saved.Spend.Own("codex", testTime); own != BoundWindow {
		t.Fatalf("five-hour spend %v after an interrupt, want the attempt's %v", own, BoundWindow)
	}
	if exists(tree) {
		t.Fatalf("tree %s is still there after the interrupt", tree)
	}

	interrupt = false
	o, err := f.runner.Resume(context.Background())
	if err != nil {
		t.Fatal(err, f.out.String())
	}
	if !o.Done {
		t.Fatalf("outcome %+v, want done", o)
	}
	if want := []string{"cell:t1", "cell:t1"}; !slices.Equal(f.calls, want) {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	if got := f.saved(t).Spend.WeeklyPct["codex"]; got != 2*BoundWeekly {
		t.Fatalf("spend codex = %v, want the interrupted attempt and the rerun, %v", got, 2*BoundWeekly)
	}
}

func TestStatusPrintsSummary(t *testing.T) {
	run := &Run{ID: "cal-1", Step: "plan", Created: testTime, Approved: &Approval{Hash: "h", Time: testTime}}
	for i := range 15 {
		state := CellPending
		if i < 3 {
			state = CellDone
		}
		run.Cells = append(run.Cells, Cell{ID: fmt.Sprintf("c%03d", i+1), Task: "t", Class: "implement", Entry: "codex/sol/medium", Round: 1, State: state})
	}
	reset := testTime.Add(2 * time.Hour)
	run.Spend = Spend{
		WeeklyPct: map[string]float64{"codex": 4},
		Windows:   map[string]WindowSpend{"codex": {ResetsAt: reset, StartUsed: 20, LastUsed: 32}},
	}
	pause := testTime.Add(time.Hour)
	run.Pause = pause
	p := Plan{CapWeekly: 10, CapWindow: 50}
	var b bytes.Buffer
	if err := WriteStatus(&b, run, p, testTime); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"calibration run cal-1", "step: plan", "done 3, pending 12, running 0",
		"codex weekly 4.0% of cap 10%", "five-hour 12.0% of cap 50%",
		"paused until " + pause.Format(time.RFC3339), "next: duolog calibrate resume",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status lacks %q:\n%s", want, out)
		}
	}

	run.Pause, run.Stopped = time.Time{}, "approved cap"
	b.Reset()
	if err := WriteStatus(&b, run, p, testTime); err != nil {
		t.Fatal(err)
	}
	if out := b.String(); !strings.Contains(out, "stopped: approved cap") || strings.Contains(out, "paused until") {
		t.Fatalf("status of a stopped run:\n%s", out)
	}
}

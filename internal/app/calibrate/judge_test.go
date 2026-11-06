package calibration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/core/config"
	corerun "github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/journal"
)

const (
	alphaPatch = "+// alpha side\n"
	betaPatch  = "+// beta side\n"
)

var implementRubric = []string{"correct against the tests and the brief", "false claims (count)"}

func entryTarget(t *testing.T, key string) config.Target {
	t.Helper()
	e, err := config.ParseEntryKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return e.Target()
}

func TestJudgeTaskThreeInputsNoSummaries(t *testing.T) {
	cfg := testConfig(t)
	c := cfg.Learning.Classes["implement"]
	task, files := JudgeTask(c, []byte(parseStatement), []byte("+reference\n"), "A: go test ./... exit 0\nB: go test ./... exit 0\n", []byte(alphaPatch), []byte(betaPatch))
	names := slices.Sorted(maps.Keys(files))
	if want := []string{"a.patch", "b.patch", "checks.txt", "reference.patch", "statement.txt"}; !slices.Equal(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	text := string(task)
	if !strings.Contains(text, "The reference patch is how the project solved it. It is evidence, not a side. Score A and B only.") {
		t.Fatalf("task text lacks the reference rule:\n%s", text)
	}
	if !strings.Contains(text, "evidence, not a side") {
		t.Fatalf("task text lacks %q:\n%s", "evidence, not a side", text)
	}
	if strings.Contains(strings.ToLower(text), "answer") {
		t.Fatalf("task text mentions an answer:\n%s", text)
	}
	for _, key := range []string{"codex/sol/high", "claude/opus/high", "codex/sol/medium"} {
		if strings.Contains(text, key) {
			t.Fatalf("task text names %s:\n%s", key, text)
		}
		for name, data := range files {
			if strings.Contains(string(data), key) {
				t.Fatalf("%s names %s", name, key)
			}
		}
	}
	for _, name := range names {
		if !strings.Contains(text, "`"+name+"`") {
			t.Fatalf("task text does not list %s:\n%s", name, text)
		}
	}
	if !strings.Contains(text, `{"scores":[{"criterion":"rubric criterion","a":0,"b":0,"count_a":0,"count_b":0,"reason":"one reason"}]}`) {
		t.Fatalf("task text lacks the verdict shape:\n%s", text)
	}
}

func TestJudgeTargetsCrossVendor(t *testing.T) {
	l := testConfig(t).Learning
	keys := func(ts []config.Target) []string {
		var out []string
		for _, target := range ts {
			out = append(out, target.Agent+"/"+target.Model+"/"+target.Effort)
		}
		return out
	}
	cases := []struct {
		a, b string
		want []string
	}{
		{"claude/opus/high", "codex/sol/high", []string{"claude/opus/high", "codex/sol/high"}},
		{"codex/sol/medium", "codex/sol/high", []string{"claude/opus/high"}},
		{"claude/opus/high", "claude/sonnet/medium", []string{"codex/sol/high"}},
	}
	for _, c := range cases {
		if got := keys(JudgeTargets(l, entryTarget(t, c.a), entryTarget(t, c.b))); !slices.Equal(got, c.want) {
			t.Errorf("JudgeTargets(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func scored(a1, b1, a2, b2 int) []profile.Scored {
	zero := 0
	return []profile.Scored{
		{Criterion: implementRubric[0], A: a1, B: b1, Reason: "tests"},
		{Criterion: implementRubric[1], A: a2, B: b2, CountA: &zero, CountB: &zero, Reason: "claims"},
	}
}

func TestCombineDisputedOnTwoPointGap(t *testing.T) {
	calls := []Call{
		{Judge: "claude/opus/high", AFirst: true, Scores: scored(3, 2, 2, 2)},
		{Judge: "codex/sol/high", AFirst: true, Scores: scored(1, 2, 2, 2)},
	}
	scores, disputed := Combine(calls, implementRubric)
	if !disputed {
		t.Fatal("A=3 by one judge and A=1 by the other is not disputed")
	}
	if len(scores) != 2 || scores[0].Original != 2 || scores[0].Profile != 2 {
		t.Fatalf("scores %+v, want the rounded means", scores)
	}
	calls[1].Scores = scored(2, 2, 2, 2)
	if _, disputed := Combine(calls, implementRubric); disputed {
		t.Fatal("a one-point gap is disputed")
	}
	// A swapped call shows B first, so its A is the pair's B.
	calls = []Call{
		{Judge: "claude/opus/high", AFirst: true, Scores: scored(3, 1, 2, 2)},
		{Judge: "codex/sol/high", AFirst: false, Scores: scored(1, 3, 2, 2)},
	}
	if _, disputed := Combine(calls, implementRubric); disputed {
		t.Fatal("a swapped call was read in its own labels")
	}
}

// judgeFixture is a run with one task whose two cells, a and b, passed.
type judgeFixture struct {
	runDir     string
	run        *Run
	task       TaskRecord
	env        JudgeEnv
	pair       Pair
	calls      []judgeCall
	slot, home string
}

type judgeCall struct {
	judge, taskPath, workRoot string
	alphaFirst                bool
	inputs                    []string
}

// scoreFunc gives the judge's scores for the alpha and the beta side.
type scoreFunc func(judge string, alphaFirst bool) (alpha1, beta1, alpha2, beta2 int)

func newJudgeFixture(t *testing.T, a, b string, score scoreFunc) *judgeFixture {
	t.Helper()
	cfg := testConfig(t)
	home := tempHome(t)
	// The project's gate goes into the judge slot, which Codex trusts.
	root := t.TempDir()
	write(t, filepath.Join(root, ".codex"), "hooks.json", gateHooks)
	slot, err := JudgeSlot(root)
	if err != nil {
		t.Fatal(err)
	}
	trustCodex(t, home, slot, true, true)
	stateDir := t.TempDir()
	run := &Run{ID: "cal-1", Step: "run", Created: testTime, Cells: []Cell{
		{ID: "c-1", Task: "k-1", Class: "implement", Entry: b, Round: 1, State: CellDone, Passed: true},
		{ID: "c-2", Task: "k-1", Class: "implement", Entry: a, Round: 1, State: CellDone, Passed: true},
	}}
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	runDir := run.Dir(stateDir)
	task := TaskRecord{ManifestTask: ManifestTask{ID: "k-1", Statement: parseStatement, Class: "implement", Verification: "hidden",
		Features: map[string]string{"output": "code"}}}
	if err := writeJSON(filepath.Join(runDir, TasksDir, task.ID, "task.json"), task); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"statement.txt": parseStatement + "\n", "reference.patch": "+reference\n"} {
		write(t, filepath.Join(runDir, TasksDir, task.ID), name, body)
	}
	for id, patch := range map[string]string{"c-1": betaPatch, "c-2": alphaPatch} {
		dir := filepath.Join(runDir, CellsDir, id)
		write(t, dir, "result.patch", patch)
		if err := writeJSON(filepath.Join(dir, "checks.json"), cellChecks{Hidden: []journal.Check{{Command: "go test ./...", Seconds: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	f := &judgeFixture{runDir: runDir, run: run, task: task, pair: Pair{Task: "k-1", Class: "implement", A: a, B: b}, slot: slot, home: home}
	f.env = JudgeEnv{
		CellEnv: CellEnv{Root: root, StateDir: stateDir, RunDir: runDir, Lead: "lead-1", Cfg: cfg,
			Deps: Deps{Environ: launchtest.Environ, Delegate: func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
				role, taskPath, opts := r.Role, r.TaskPath, r.Options
				// Calibration keeps the task copy in the work root's state,
				// where task.Inline reads it.
				if !opts.Judge || !opts.Calibration || opts.Entry == nil || opts.Lead != "lead-1" {
					t.Errorf("options %+v", opts)
				}
				if role != "review_final" {
					t.Errorf("role %q, want review_final", role)
				}
				judge := opts.Entry.Agent + "/" + opts.Entry.Model + "/" + opts.Entry.Effort
				first, err := os.ReadFile(filepath.Join(opts.WorkRoot, "a.patch"))
				if err != nil {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(opts.WorkRoot)
				if err != nil {
					t.Fatal(err)
				}
				// The slot's own .git and .codex are not inputs.
				var inputs []string
				for _, e := range entries {
					if !strings.HasPrefix(e.Name(), ".") {
						inputs = append(inputs, e.Name())
					}
				}
				alphaFirst := string(first) == alphaPatch
				f.calls = append(f.calls, judgeCall{judge: judge, taskPath: taskPath, workRoot: opts.WorkRoot, alphaFirst: alphaFirst, inputs: inputs})
				a1, b1, a2, b2 := score(judge, alphaFirst)
				if !alphaFirst {
					a1, b1, a2, b2 = b1, a1, b2, a2
				}
				summary := fmt.Sprintf(`Done. {"scores":[{"criterion":%q,"a":%d,"b":%d,"reason":"tests"},{"criterion":%q,"a":%d,"b":%d,"count_a":0,"count_b":1,"reason":"claims"}]}`,
					implementRubric[0], a1, b1, implementRubric[1], a2, b2)
				return delegate.Outcome{Result: corerun.Result{Done: true, Summary: summary}}, nil
			}}},
		Journal: &journal.Journal{Dir: t.TempDir()},
	}
	f.env.Rand = func() bool { return true }
	return f
}

func (f *judgeFixture) judge(t *testing.T) journal.Verdict {
	t.Helper()
	v, err := JudgePair(context.Background(), f.env, f.run, f.pair, f.task)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *judgeFixture) verdicts(t *testing.T) []journal.Verdict {
	t.Helper()
	vs, err := journal.ReadAll[journal.Verdict](f.env.Journal, "verdicts.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	return vs
}

func TestCloseOrDisputedJudgedSwapped(t *testing.T) {
	// Same agent: one judge of the other agent. Means 2.0 vs 2.5 are close.
	var swapped bool
	f := newJudgeFixture(t, "codex/sol/high", "codex/sol/medium", func(judge string, alphaFirst bool) (int, int, int, int) {
		if !alphaFirst {
			swapped = true
			return 3, 1, 2, 2 // the winner reverses with the order
		}
		return 2, 3, 2, 2
	})
	v := f.judge(t)
	if len(f.calls) != 2 {
		t.Fatalf("%d judge calls, want 2", len(f.calls))
	}
	if !f.calls[0].alphaFirst || f.calls[1].alphaFirst || !swapped {
		t.Fatalf("calls %+v: the second call must flip the order", f.calls)
	}
	if f.calls[0].judge != "claude/opus/high" || f.calls[1].judge != "claude/opus/high" {
		t.Fatalf("calls %+v, want the same judge twice", f.calls)
	}
	if !v.Disputed {
		t.Fatalf("verdict %+v: a winner reversal is not disputed", v)
	}

	// A clear result needs no second call.
	clear := newJudgeFixture(t, "codex/sol/high", "codex/sol/medium", func(string, bool) (int, int, int, int) { return 3, 1, 3, 1 })
	v = clear.judge(t)
	if len(clear.calls) != 1 || v.Disputed {
		t.Fatalf("%d calls, disputed %v; want 1 call, not disputed", len(clear.calls), v.Disputed)
	}
}

func TestOneVerdictPerPairPerTask(t *testing.T) {
	f := newJudgeFixture(t, "claude/opus/high", "codex/sol/high", func(judge string, _ bool) (int, int, int, int) {
		if judge == "claude/opus/high" {
			return 3, 2, 2, 2
		}
		return 2, 2, 2, 2
	})
	v := f.judge(t)
	if len(f.calls) != 4 {
		t.Fatalf("%d judge calls, want 4", len(f.calls))
	}
	vs := f.verdicts(t)
	if len(vs) != 1 {
		t.Fatalf("%d verdicts appended, want 1", len(vs))
	}
	got := vs[0]
	if got.ID != v.ID || got.Kind != "verdict" || got.Source != "calibration" || got.Run != "cal-1" || got.Task != "k-1" ||
		got.Class != "implement" || got.Verification != "hidden" || got.Ladder != "implement" {
		t.Fatalf("verdict %+v", got)
	}
	if got.Original != "claude/opus/high" || got.Profile != "codex/sol/high" {
		t.Fatalf("Original %q Profile %q, want A and B", got.Original, got.Profile)
	}
	if got.Judge != "claude/opus/high,codex/sol/high" {
		t.Fatalf("Judge %q", got.Judge)
	}
	if got.Disputed {
		t.Fatal("consistent judges gave a disputed verdict")
	}
	// (3+2+3+2)/4 = 2.5 rounds to 3.
	if len(got.Scores) != 2 || got.Scores[0].Original != 3 || got.Scores[0].Profile != 2 || got.Scores[1].Original != 2 || got.Scores[1].Profile != 2 {
		t.Fatalf("scores %+v, want rounded means 3/2 and 2/2", got.Scores)
	}
	if len(got.OriginalChecks) != 1 || len(got.ProfileChecks) != 1 {
		t.Fatalf("checks %+v %+v", got.OriginalChecks, got.ProfileChecks)
	}
}

func TestPairsOnlyBothPass(t *testing.T) {
	run := &Run{Cells: []Cell{
		{ID: "c-1", Task: "t1", Class: "implement", Entry: "codex/sol/high", State: CellDone, Passed: true},
		{ID: "c-2", Task: "t1", Class: "implement", Entry: "claude/opus/high", State: CellDone},
		{ID: "c-3", Task: "t2", Class: "implement", Entry: "codex/sol/high", State: CellDone, Passed: true},
		{ID: "c-4", Task: "t2", Class: "implement", Entry: "claude/opus/high", State: CellDone, Passed: true},
		{ID: "c-5", Task: "t3", Class: "implement", Entry: "codex/sol/high", State: CellDone, Passed: true},
		{ID: "c-6", Task: "t3", Class: "implement", Entry: "claude/opus/high", State: CellLeakage, Passed: true},
	}}
	got := Pairs(run)
	want := []Pair{{Task: "t2", Class: "implement", A: "claude/opus/high", B: "codex/sol/high"}}
	if !slices.Equal(got, want) {
		t.Fatalf("Pairs = %+v, want %+v", got, want)
	}
}

func TestJudgeFilesNotMarkdown(t *testing.T) {
	f := newJudgeFixture(t, "claude/opus/high", "codex/sol/high", func(string, bool) (int, int, int, int) { return 2, 2, 2, 2 })
	v := f.judge(t)
	judgeDir := filepath.Join(f.runDir, "judge")
	for _, call := range f.calls {
		if call.taskPath != filepath.Join(judgeDir, v.ID, "task.md") {
			t.Fatalf("task %s, want runs/<run>/judge/<verdict-id>/task.md", call.taskPath)
		}
		if call.workRoot != f.slot {
			t.Fatalf("work root %s, want the judge slot %s", call.workRoot, f.slot)
		}
		if want := []string{"a.patch", "b.patch", "checks.txt", "reference.patch", "statement.txt"}; !slices.Equal(call.inputs, want) {
			t.Fatalf("work root holds %v, want %v", call.inputs, want)
		}
	}
	// The slot keeps only the hooks the person approved.
	entries, err := os.ReadDir(f.slot)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if want := []string{".codex"}; !slices.Equal(left, want) {
		t.Fatalf("judge slot holds %v after judging, want %v", left, want)
	}
	var tasks, answers int
	err = filepath.WalkDir(judgeDir, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch {
		case strings.HasSuffix(name, ".md") && d.Name() != "task.md":
			t.Errorf("%s is Markdown", name)
		case d.Name() == "task.md":
			tasks++
		case d.Name() == "answer.json":
			answers++
			var answer struct{ Summary string }
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &answer); err != nil || !strings.Contains(answer.Summary, "scores") {
				t.Errorf("%s: %v, %q", name, err, answer.Summary)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if tasks != 1 || answers != len(f.calls) {
		t.Fatalf("%d task.md and %d answer.json for %d calls", tasks, answers, len(f.calls))
	}
}

// A Codex judge runs only in a judge slot Codex trusts with its hooks.
func TestJudgeRefusesUntrustedSlot(t *testing.T) {
	f := newJudgeFixture(t, "claude/opus/high", "claude/sonnet/medium", func(string, bool) (int, int, int, int) { return 3, 1, 3, 1 })
	trustCodex(t, f.home, f.slot, true, false)
	_, err := JudgePair(context.Background(), f.env, f.run, f.pair, f.task)
	if err == nil || !strings.Contains(err.Error(), "Codex must trust") || !strings.Contains(err.Error(), f.slot) {
		t.Fatalf("err = %v, want a trust refusal naming %s", err, f.slot)
	}
	if len(f.calls) != 0 {
		t.Fatalf("%d judge calls in an untrusted slot", len(f.calls))
	}
	// The hooks stay in the slot for the person to approve.
	if _, err := os.Stat(filepath.Join(f.slot, ".codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
}

func clearScores(string, bool) (int, int, int, int) { return 3, 1, 3, 1 }

// runner is a Runner over the fixture's run with quota q.
func (f *judgeFixture) runner(q *fakeQuota, out *bytes.Buffer) *Runner {
	return &Runner{StateDir: f.env.StateDir, Root: f.env.Root, Cfg: f.env.Cfg, Run: f.run,
		Quota: &QuotaCache{Read: q.read, Now: func() time.Time { return testTime }}, Now: func() time.Time { return testTime }, Out: out}
}

// At the lead reserve a judge call is refused before it runs, and the stop
// is saved.
func TestJudgeStopsBeforeCallAtLeadReserve(t *testing.T) {
	f := newJudgeFixture(t, "claude/opus/high", "codex/sol/high", clearScores)
	var out bytes.Buffer
	r := f.runner(&fakeQuota{weekly: 85, window: 20, reset: testTime.Add(time.Hour)}, &out)
	o, vs, err := r.Judge(context.Background(), f.env, []Pair{f.pair}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Stopped != "lead reserve" || len(vs) != 0 || len(f.calls) != 0 {
		t.Fatalf("outcome %+v, %d verdicts, %d calls; want a stop at the lead reserve before any call", o, len(vs), len(f.calls))
	}
	saved, err := LoadRun(f.env.StateDir, f.run.ID)
	if err != nil || saved.Stopped != "lead reserve" {
		t.Fatalf("saved run %+v, %v", saved, err)
	}
	if !strings.Contains(out.String(), "stopped: lead reserve") {
		t.Fatalf("output %q", out.String())
	}
}

// Every judge call is charged to the run's spend, as a cell is.
func TestJudgeChargesEveryCall(t *testing.T) {
	f := newJudgeFixture(t, "claude/opus/high", "codex/sol/high", clearScores)
	r := f.runner(&fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}, &bytes.Buffer{})
	o, vs, err := r.Judge(context.Background(), f.env, []Pair{f.pair}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Done || len(vs) != 1 || len(f.calls) != 2 {
		t.Fatalf("outcome %+v, %d verdicts, %d calls; want one verdict of 2 calls", o, len(vs), len(f.calls))
	}
	saved, err := LoadRun(f.env.StateDir, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// No rate and no movement: each call counts at the conservative bound.
	for _, agent := range []string{"claude", "codex"} {
		if got := saved.Spend.WeeklyPct[agent]; got != BoundWeekly {
			t.Fatalf("%s weekly spend %v, want %v", agent, got, BoundWeekly)
		}
	}
}

// A judge call is charged before it starts, so a call that fails, or one a
// Ctrl-C ends, stays counted once.
func TestJudgeChargesCallBeforeItStarts(t *testing.T) {
	f := newJudgeFixture(t, "claude/opus/high", "codex/sol/high", clearScores)
	inner := f.env.Delegate
	var during float64
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		saved, err := LoadRun(f.env.StateDir, f.run.ID)
		if err != nil {
			t.Fatal(err)
		}
		agent := r.Options.Entry.Agent
		during = saved.Spend.WeeklyPct[agent]
		if _, err := inner(ctx, r); err != nil {
			return delegate.Outcome{}, err
		}
		return delegate.Outcome{}, errors.New("worker failed")
	}
	r := f.runner(&fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}, &bytes.Buffer{})
	if _, _, err := r.Judge(context.Background(), f.env, []Pair{f.pair}, nil); err == nil || !strings.Contains(err.Error(), "worker failed") {
		t.Fatalf("err = %v, want the failed call", err)
	}
	if during != BoundWeekly {
		t.Fatalf("spend while the call ran = %v, want %v", during, BoundWeekly)
	}
	saved, err := LoadRun(f.env.StateDir, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	agent := strings.SplitN(f.calls[0].judge, "/", 2)[0]
	if got := saved.Spend.WeeklyPct[agent]; got != BoundWeekly {
		t.Fatalf("%s weekly spend after the failed call = %v, want %v", agent, got, BoundWeekly)
	}
}

// A judge call the delegation refuses before its worker starts is not
// charged: the admitted charge is released.
func TestJudgeReleasesCallNotStarted(t *testing.T) {
	f := newJudgeFixture(t, "claude/opus/high", "codex/sol/high", clearScores)
	f.env.Delegate = func(context.Context, delegate.Request) (delegate.Outcome, error) {
		return delegate.Outcome{}, &delegate.NotStartedError{Err: errors.New("lead lead-1 is not registered")}
	}
	r := f.runner(&fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}, &bytes.Buffer{})
	if _, _, err := r.Judge(context.Background(), f.env, []Pair{f.pair}, nil); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("err = %v, want the refusal", err)
	}
	saved, err := LoadRun(f.env.StateDir, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		if saved.Spend.WeeklyPct[agent] != 0 || saved.Spend.JudgePct[agent] != 0 {
			t.Fatalf("%s spend %+v after a call that never started, want none", agent, saved.Spend)
		}
	}
	for key, o := range saved.Spend.Outstanding {
		if len(o.Charges) > 0 {
			t.Fatalf("window %s keeps charges %+v", key, o.Charges)
		}
	}
}

// A quota refresh that fails after a call stops judging before the next
// call; the pair keeps the call it made.
func TestJudgeStopsWhenRefreshFails(t *testing.T) {
	f := newJudgeFixture(t, "claude/opus/high", "codex/sol/high", clearScores)
	var out bytes.Buffer
	// Read 1 admits the first call; read 2, its refresh, fails.
	r := f.runner(&fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour), failAt: 2}, &out)
	o, vs, err := r.Judge(context.Background(), f.env, []Pair{f.pair}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Stopped != "quota unavailable" || len(vs) != 0 || len(f.calls) != 1 {
		t.Fatalf("outcome %+v, %d verdicts, %d calls; want a stop after one call", o, len(vs), len(f.calls))
	}
	saved, err := LoadRun(f.env.StateDir, f.run.ID)
	if err != nil || saved.Stopped != "quota unavailable" {
		t.Fatalf("saved run %+v, %v", saved, err)
	}
	if !strings.Contains(out.String(), "stopped: quota unavailable") {
		t.Fatalf("output %q", out.String())
	}
	_, _, st, err := openPair(filepath.Join(f.env.RunDir, JudgeDir), f.env.Rand, f.pair, []string{"claude/opus/high", "codex/sol/high"},
		hashOf(f.env.Cfg.Learning.Classes["implement"].Rubric))
	if err != nil || len(st.Calls) != 1 {
		t.Fatalf("pair keeps %d calls, %v; want 1", len(st.Calls), err)
	}
}

// TestJudgeOrderFromRand: a new pair's first order is what Rand of Deps says.
func TestJudgeOrderFromRand(t *testing.T) {
	t.Parallel()
	p := Pair{Task: "k-1", Class: "implement", A: "codex/sol/high", B: "codex/terra/high"}
	for _, aFirst := range []bool{true, false} {
		_, _, st, err := openPair(t.TempDir(), func() bool { return aFirst }, p, []string{"claude/opus/high"}, "rubric")
		if err != nil || st.AFirst != aFirst {
			t.Fatalf("AFirst = %v, %v; want %v", st.AFirst, err, aFirst)
		}
	}
}

// A pair whose judges changed after its first call is judged again from the
// start with the new judges, not finished with calls of two schedules.
func TestJudgeRestartsPairWhenJudgesChange(t *testing.T) {
	f := newJudgeFixture(t, "claude/opus/high", "codex/sol/high", clearScores)
	// Read 1 admits the first call; read 2, its refresh, fails.
	q := &fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour), failAt: 2}
	o, _, err := f.runner(q, &bytes.Buffer{}).Judge(context.Background(), f.env, []Pair{f.pair}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Stopped != "quota unavailable" || len(f.calls) != 1 || f.calls[0].judge != "claude/opus/high" {
		t.Fatalf("outcome %+v, calls %+v; want one claude call saved", o, f.calls)
	}

	slices.Reverse(f.env.Cfg.Learning.Judges)
	run, err := LoadRun(f.env.StateDir, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.run = run
	q = &fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}
	o, vs, err := f.runner(q, &bytes.Buffer{}).Judge(context.Background(), f.env, []Pair{f.pair}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Done || len(vs) != 1 {
		t.Fatalf("outcome %+v, %d verdicts; want one", o, len(vs))
	}
	var judges []string
	for _, c := range f.calls[1:] {
		judges = append(judges, c.judge)
	}
	if want := []string{"codex/sol/high", "claude/opus/high"}; !slices.Equal(judges, want) {
		t.Fatalf("calls after the change %v, want the new schedule %v from the start", judges, want)
	}
	if vs[0].Judge != "codex/sol/high,claude/opus/high" || !strings.Contains(vs[0].Note, "judged again from the start") {
		t.Fatalf("verdict judge %q, note %q; want the new judges and a note", vs[0].Judge, vs[0].Note)
	}
}

// Without a rate a judge call counts the bound scaled by its tokens over the
// class's cell estimate, not a whole cell.
func TestJudgeCallCostScaledByTokens(t *testing.T) {
	f := newJudgeFixture(t, "codex/sol/high", "codex/sol/medium", clearScores)
	r := f.runner(&fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}, &bytes.Buffer{})
	r.Manifest = Manifest{Tasks: []ManifestTask{{ID: "k-1", Class: "implement"}}, Estimates: map[string]Estimate{"k-1": {TokensHigh: 1_000_000}}}
	if _, _, err := r.Judge(context.Background(), f.env, []Pair{f.pair}, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := LoadRun(f.env.StateDir, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := BoundWeekly * judgeTokensHigh / 1_000_000
	if got := saved.Spend.WeeklyPct["claude"]; !near(got, want) {
		t.Fatalf("claude weekly spend %v for one judge call, want %v", got, want)
	}
}

// A judge call that moves the reading gives the judge's agent a rate, so the
// plan and the next calls need not count it at the bound.
func TestJudgeRecordsRateFromCallRise(t *testing.T) {
	f := newJudgeFixture(t, "codex/sol/high", "codex/sol/medium", clearScores)
	q := &fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}
	inner := f.env.Delegate
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		out, err := inner(ctx, r)
		q.weekly, q.window = q.weekly+0.2, q.window+1
		out.Result.Usage = corerun.Usage{Input: 80000, Output: 20000}
		return out, err
	}
	if _, _, err := f.runner(q, &bytes.Buffer{}).Judge(context.Background(), f.env, []Pair{f.pair}, nil); err != nil {
		t.Fatal(err)
	}
	rates, err := ReadRates(f.env.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := rates["claude"]; !near(got.WeeklyPerMTok, 2) || !near(got.WindowPerMTok, 10) {
		t.Fatalf("claude rate %+v, want 2%% weekly and 10%% five-hour per 1M tokens", got)
	}
}

// A judge call that moved no weekly quota sets no rate: the agent keeps the
// scaled cost of an unknown rate, and a later call that moves the reading
// gives it one.
func TestJudgeSetsNoRateWithoutWeeklyRise(t *testing.T) {
	f := newJudgeFixture(t, "codex/sol/high", "codex/sol/medium", clearScores)
	q := &fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}
	weekly := 0.0
	inner := f.env.Delegate
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		out, err := inner(ctx, r)
		q.weekly, q.window = q.weekly+weekly, q.window+1
		out.Result.Usage = corerun.Usage{Input: 80000, Output: 20000}
		return out, err
	}
	if _, _, err := f.runner(q, &bytes.Buffer{}).Judge(context.Background(), f.env, []Pair{f.pair}, nil); err != nil {
		t.Fatal(err)
	}
	rates, err := ReadRates(f.env.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := rates["claude"]; ok {
		t.Fatalf("claude rate %+v from a call that moved no weekly quota, want none", got)
	}
	weekly = 0.2
	if _, _, err := f.runner(q, &bytes.Buffer{}).Judge(context.Background(), f.env, []Pair{f.pair}, nil); err != nil {
		t.Fatal(err)
	}
	if rates, err = ReadRates(f.env.StateDir); err != nil {
		t.Fatal(err)
	}
	if got := rates["claude"]; !near(got.WeeklyPerMTok, 2) || !near(got.WindowPerMTok, 10) {
		t.Fatalf("claude rate %+v, want 2%% weekly and 10%% five-hour per 1M tokens", got)
	}
}

// Every judge call or rewrite gets a fresh repository in the judge slot, so
// what a worker wrote into .git does not reach the next call.
func TestJudgeSlotFreshGitPerCall(t *testing.T) {
	tempHome(t)
	root := t.TempDir()
	write(t, filepath.Join(root, ".codex"), "hooks.json", gateHooks)
	slot, err := JudgeSlot(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cleanup, err := NewJudgeSlot(ctx, root, slot, nil, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(slot, ".git", "hooks"), "post-checkout", "#!/bin/sh\n")
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJudgeSlot(ctx, root, slot, nil, os.Environ()); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(slot, ".git", "hooks", "post-checkout")) {
		t.Fatal("a worker's .git change reached the next call")
	}
	if !exists(filepath.Join(slot, ".git", "HEAD")) {
		t.Fatal("the judge slot has no repository")
	}
}

// A pair stopped between its calls keeps the calls it made; judging again
// makes only the rest, in the order the pair started with.
func TestJudgeResumesFromSavedCalls(t *testing.T) {
	reversing := func(judge string, alphaFirst bool) (int, int, int, int) {
		if !alphaFirst {
			return 3, 1, 2, 2
		}
		return 2, 3, 2, 2
	}
	f := newJudgeFixture(t, "codex/sol/high", "codex/sol/medium", reversing)
	// The cap leaves room for one call at the bound.
	f.env.Cfg.Calibration.MaxWeeklyShare = 0.01
	q := &fakeQuota{weekly: 20, window: 20, reset: testTime.Add(time.Hour)}
	o, vs, err := f.runner(q, &bytes.Buffer{}).Judge(context.Background(), f.env, []Pair{f.pair}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.Stopped != "approved cap" || len(vs) != 0 || len(f.calls) != 1 {
		t.Fatalf("outcome %+v, %d verdicts, %d calls; want a stop after one call", o, len(vs), len(f.calls))
	}
	if journaled := f.verdicts(t); len(journaled) != 0 {
		t.Fatalf("a half-judged pair was journalled: %+v", journaled)
	}

	f.env.Cfg.Calibration.MaxWeeklyShare = 0.10
	f.env.Rand = func() bool { return false }
	run, err := LoadRun(f.env.StateDir, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.run = run
	o, vs, err = f.runner(q, &bytes.Buffer{}).Judge(context.Background(), f.env, []Pair{f.pair}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Done || len(vs) != 1 || len(f.calls) != 2 {
		t.Fatalf("outcome %+v, %d verdicts, %d calls; want the one call left", o, len(vs), len(f.calls))
	}
	if !f.calls[0].alphaFirst || f.calls[1].alphaFirst {
		t.Fatalf("calls %+v: the second call must swap the saved order", f.calls)
	}
	if !vs[0].Disputed {
		t.Fatalf("verdict %+v: the calls of both runs did not combine", vs[0])
	}
	saved, err := LoadRun(f.env.StateDir, f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Spend.WeeklyPct["claude"]; got != 2*BoundWeekly {
		t.Fatalf("claude weekly spend %v, want both calls", got)
	}
}

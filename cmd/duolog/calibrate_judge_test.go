package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/calibration"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// tempCache points the user cache at a fresh folder, so the calibration
// slots never land in the person's own.
func tempCache(t *testing.T) {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)
}

// claudeJudges makes claude/opus/high the only judge, so a cross-vendor pair
// takes one call per order.
func claudeJudges(t *testing.T, root string) {
	t.Helper()
	config := "learning:\n  enabled: true\n  judges: [{agent: claude, model: opus, effort: high}]\n"
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

// claudeAnswer is a fake claude -p output whose summary is summary.
func claudeAnswer(t *testing.T, summary string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"structured_output": map[string]any{"done": true, "summary": summary, "files": []string{}, "needs": []string{}},
		"usage": map[string]int{"input_tokens": 100, "output_tokens": 50}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// trustJudgeSlot gives the project in the current folder the duolog gate in
// .codex/hooks.json and writes a Codex config under the temporary home that
// trusts the judge slot and approves the gate there, as the person does once.
func trustJudgeSlot(t *testing.T) {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	hooks := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex", "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	slot, err := calibration.JudgeSlot(root)
	if err != nil {
		t.Fatal(err)
	}
	trust := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n",
		slot, filepath.Join(slot, ".codex", "hooks.json")+":pre_tool_use:0:0")
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(trust), 0o600); err != nil {
		t.Fatal(err)
	}
}

func samePath(a, b string) bool {
	return pathutil.Clean(pathutil.Resolved(a)) == pathutil.Clean(pathutil.Resolved(b))
}

// The judge runs through the real delegation: the task copy is inlined from
// the judge slot, and the worker runs there with the inputs inlined.
func TestCalibrateJudgeRunsRealDelegation(t *testing.T) {
	root := learningRepo(t, true)
	tempCache(t)
	fakeQuota(t)
	claudeJudges(t, root)
	stateDir := filepath.Join(root, ".duolog")
	ranRun(t, stateDir)
	summary := `{"scores":[{"criterion":"correct against the tests and the brief","a":3,"b":1,"reason":"tests"},` +
		`{"criterion":"false claims (count)","a":3,"b":1,"count_a":0,"count_b":2,"reason":"claims"}]}`
	fake := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"auth status": {Stdout: `{"loggedIn":true}`},
		"-p":          {Stdout: claudeAnswer(t, summary)},
	}})
	out, err := executeBusCommand(t, "calibrate", "judge", "--lead", "task-lead")
	if err != nil {
		t.Fatal(out, err)
	}
	if !strings.Contains(out, "class implement: 2 verdicts") {
		t.Fatalf("output lacks two verdicts:\n%s", out)
	}
	slot, err := calibration.JudgeSlot(root)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.ReadFile(fake + ".cwd")
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(strings.TrimSpace(string(cwd)), slot) {
		t.Fatalf("judge ran in %s, want the judge slot %s", cwd, slot)
	}
	stdin, err := os.ReadFile(fake + ".stdin")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Add a function Valuet2 that returns its number.", "+reference", "Score every criterion"} {
		if !strings.Contains(string(stdin), want) {
			t.Fatalf("worker input lacks %q:\n%s", want, stdin)
		}
	}
	// The slot keeps no input of the last call.
	if _, err := os.Stat(filepath.Join(slot, "reference.patch")); !os.IsNotExist(err) {
		t.Fatalf("judge slot keeps its inputs: %v", err)
	}
	latest, _, err := calibration.LatestRun(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	// Two calls at the conservative bound, one per pair.
	if got := latest.Spend.WeeklyPct["claude"]; got != 2*calibration.BoundWeekly {
		t.Fatalf("claude weekly spend %v, want %v", got, 2*calibration.BoundWeekly)
	}
}

// A judge call waits for admission: at the lead reserve no call runs, the
// stop is saved and judging stays the next step.
func TestCalibrateJudgeStopsAtLeadReserve(t *testing.T) {
	root := learningRepo(t, true)
	tempCache(t)
	stateDir := filepath.Join(root, ".duolog")
	ranRun(t, stateDir)
	calls := fakeJudge(t)
	old := quotaReader
	quotaReader = func(ctx context.Context, agent string) ([]calibration.Window, error) {
		return []calibration.Window{{Kind: calibration.KindWeekly, UsedPercent: 85}}, nil
	}
	t.Cleanup(func() { quotaReader = old })
	out, err := executeBusCommand(t, "calibrate", "judge")
	if err != nil {
		t.Fatal(out, err)
	}
	if *calls != 0 {
		t.Fatalf("%d judge calls past the lead reserve", *calls)
	}
	for _, want := range []string{"stopped: lead reserve", "next: duolog calibrate judge"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	latest, _, err := calibration.LatestRun(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Stopped != "lead reserve" || latest.Step != "run" {
		t.Fatalf("run stopped %q at step %q, want lead reserve at run", latest.Stopped, latest.Step)
	}
}

// rewriteFixture is a run folder with one task to rewrite.
func rewriteFixture(t *testing.T, root string) (*config.Config, *store.Store, string, calibration.TaskRecord) {
	t.Helper()
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := calibration.NewRun(s.Dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runDir := run.Dir(s.Dir)
	task := calibration.TaskRecord{ManifestTask: calibration.ManifestTask{ID: "t1", Class: "implement"}, Subject: "add the parser"}
	dir := filepath.Join(runDir, calibration.TasksDir, task.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reference.patch"), []byte("+func Parse() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, s, runDir, task
}

// A rewrite runs in the judge slot, not in a fresh folder no trust covers.
func TestRewriteRunsInJudgeSlot(t *testing.T) {
	root := learningRepo(t, true)
	tempCache(t)
	claudeJudges(t, root)
	fake := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"auth status": {Stdout: `{"loggedIn":true}`},
		"-p":          {Stdout: claudeAnswer(t, "Add a function Parse that reads the format.")},
	}})
	cfg, s, runDir, task := rewriteFixture(t, root)
	statement, _, err := rewriteStatement(root, cfg, s, runDir, "task-lead")(context.Background(), task, nil)
	if err != nil {
		t.Fatal(err)
	}
	if statement != "Add a function Parse that reads the format." {
		t.Fatalf("statement %q", statement)
	}
	slot, err := calibration.JudgeSlot(root)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.ReadFile(fake + ".cwd")
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(strings.TrimSpace(string(cwd)), slot) {
		t.Fatalf("rewrite ran in %s, want the judge slot %s", cwd, slot)
	}
	stdin, err := os.ReadFile(fake + ".stdin")
	if err != nil || !strings.Contains(string(stdin), "+func Parse() {}") {
		t.Fatalf("worker input lacks the reference: %v\n%s", err, stdin)
	}
	// The rewrite worker's settings deny the telemetry and balance roots.
	args, err := os.ReadFile(fake + ".args.json")
	if err != nil {
		t.Fatal(err)
	}
	roots, err := gateProtectedRoots()
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		if !strings.Contains(string(args), filepath.ToSlash(root)) {
			t.Fatalf("rewrite worker argv lacks %s:\n%s", root, args)
		}
	}
}

// A Codex rewrite in a slot Codex does not trust is refused before any call.
func TestRewriteRefusesUntrustedSlot(t *testing.T) {
	root := learningRepo(t, true)
	tempCache(t)
	config := "learning:\n  enabled: true\n  judges: [{agent: codex, model: sol, effort: high}]\n"
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := testbin.Install(t, "codex", testbin.Scenario{})
	cfg, s, runDir, task := rewriteFixture(t, root)
	_, _, err := rewriteStatement(root, cfg, s, runDir, "task-lead")(context.Background(), task, nil)
	slot, serr := calibration.JudgeSlot(root)
	if serr != nil {
		t.Fatal(serr)
	}
	if err == nil || !strings.Contains(err.Error(), "Codex must trust") || !strings.Contains(err.Error(), slot) {
		t.Fatalf("err = %v, want a trust refusal naming %s", err, slot)
	}
	if _, err := os.Stat(fake + ".args.json"); !os.IsNotExist(err) {
		t.Fatalf("codex ran in an untrusted slot: %v", err)
	}
}

// A rewrite Codex refuses in the judge slot costs nothing: the run checks
// the slot before it admits and charges the rewrite.
func TestRunRewriteTrustRefusalChargesNothing(t *testing.T) {
	root := learningRepo(t, true)
	tempCache(t)
	fakeQuota(t)
	config := "learning:\n  enabled: true\n  judges: [{agent: codex, model: sol, effort: high}]\n"
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := testbin.Install(t, "codex", testbin.Scenario{})
	cfg, s, runDir, task := rewriteFixture(t, root)
	run, _, err := calibration.LatestRun(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	task.Statement, task.NeedsRewrite = "Add the parser.", true
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, calibration.TasksDir, task.ID, "task.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	m := calibration.Manifest{Tasks: []calibration.ManifestTask{task.ManifestTask}, Rewrite: []string{task.ID}}
	if err := calibration.SaveManifest(runDir, m); err != nil {
		t.Fatal(err)
	}
	run.Step, run.Approved = "plan", &calibration.Approval{Hash: "h", Time: time.Now()}
	run.Cells = []calibration.Cell{{ID: "c001", Task: task.ID, Class: "implement", Entry: "codex/sol/medium", Round: 1, State: calibration.CellPending}}
	if err := run.Save(s.Dir); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	r := &calibration.Runner{StateDir: s.Dir, Root: root, Cfg: cfg, Run: run, Plan: calibration.Plan{Run: run.ID, CapWeekly: 10, CapWindow: 50}, Manifest: m,
		Quota: &calibration.QuotaCache{Read: quotaReader}, Now: time.Now,
		Cell: func(context.Context, calibration.Cell, calibration.TaskRecord, calibration.StartFunc) (calibration.CellResult, error) {
			t.Fatal("a cell ran before its statement was rewritten")
			return calibration.CellResult{}, nil
		},
		Rewrite: rewriteStatement(root, cfg, s, runDir, "task-lead"), Out: &out}
	if _, err := r.Execute(context.Background()); err == nil || !strings.Contains(err.Error(), "Codex must trust") {
		t.Fatalf("Execute = %v, want a trust refusal:\n%s", err, out.String())
	}
	if _, err := os.Stat(fake + ".args.json"); !os.IsNotExist(err) {
		t.Fatalf("codex ran in an untrusted slot: %v", err)
	}
	latest, _, err := calibration.LatestRun(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := latest.Spend.WeeklyPct["codex"]; got != 0 {
		t.Fatalf("codex weekly spend %v after a refused rewrite, want 0", got)
	}
}

// Judging keeps to the judges and rubrics of the approved plan: a changed
// judge list is refused until a new plan is approved.
func TestCalibrateJudgeRefusesChangedJudges(t *testing.T) {
	root := learningRepo(t, true)
	calls := fakeJudge(t)
	stateDir := filepath.Join(root, ".duolog")
	run := ranRun(t, stateDir)
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	runDir := run.Dir(stateDir)
	m, err := calibration.LoadManifest(runDir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := calibration.BuildPlan(calibration.PlanInput{Cfg: cfg, Manifest: m, RunID: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := calibration.SavePlan(runDir, p); err != nil {
		t.Fatal(err)
	}
	run.Approved = &calibration.Approval{Hash: p.Hash, Time: time.Now()}
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	claudeJudges(t, root)
	_, err = executeBusCommand(t, "calibrate", "judge")
	if err == nil || !strings.Contains(err.Error(), "judges changed since approval; plan again") {
		t.Fatalf("err = %v, want the changed judges refused", err)
	}
	if *calls != 0 {
		t.Fatalf("%d judge calls with judges the person did not approve", *calls)
	}
}

// Judges changed after the cells ran are no dead end: the plan is shown and
// approved again at step run or judge, the cells that ran and the spend carry
// over, and judging goes on under the new approval.
func TestCalibratePlanAgainAfterJudgesChanged(t *testing.T) {
	for _, step := range []string{"run", "judge"} {
		t.Run(step, func(t *testing.T) {
			root := learningRepo(t, true)
			calls := fakeJudge(t)
			ladder := "  classes:\n    implement:\n      ladder: [{agent: claude, model: opus, effort: high}, {agent: codex, model: sol, effort: high}]\n"
			writeConfig := func(judges string) {
				t.Helper()
				data := "learning:\n  enabled: true\n" + judges + ladder
				if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			writeConfig("")
			stateDir := filepath.Join(root, ".duolog")
			run := ranRun(t, stateDir)
			cfg, err := configfile.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			runDir := run.Dir(stateDir)
			m, err := calibration.LoadManifest(runDir)
			if err != nil {
				t.Fatal(err)
			}
			p, err := calibration.BuildPlan(calibration.PlanInput{Cfg: cfg, Manifest: m, RunID: run.ID})
			if err != nil {
				t.Fatal(err)
			}
			if err := calibration.SavePlan(runDir, p); err != nil {
				t.Fatal(err)
			}
			run.Step, run.Approved = step, &calibration.Approval{Hash: p.Hash, Time: time.Now()}
			run.Spend.WeeklyPct = map[string]float64{"claude": 1, "codex": 1}
			if err := run.Save(stateDir); err != nil {
				t.Fatal(err)
			}
			writeConfig("  judges: [{agent: claude, model: opus, effort: high}]\n")
			if _, err := executeBusCommand(t, "calibrate", "judge"); err == nil || !strings.Contains(err.Error(), "plan again") {
				t.Fatalf("err = %v, want the changed judges refused", err)
			}
			out, err := executeBusCommand(t, "calibrate", "plan")
			if err != nil {
				t.Fatal(out, err)
			}
			const prefix = "approve with: duolog calibrate plan --approve "
			i := strings.Index(out, prefix)
			if i < 0 {
				t.Fatalf("output lacks %q:\n%s", prefix, out)
			}
			hash := strings.TrimSpace(strings.SplitN(out[i+len(prefix):], "\n", 2)[0])
			out, err = executeBusCommand(t, "calibrate", "plan", "--approve", hash)
			if err != nil {
				t.Fatal(out, err)
			}
			if !strings.Contains(out, "next: duolog calibrate judge") {
				t.Fatalf("approval output lacks the judge step:\n%s", out)
			}
			latest, _, err := calibration.LatestRun(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			if latest.Approved == nil || latest.Approved.Hash != hash || latest.Step != "run" {
				t.Fatalf("approval %+v at step %q, want %s at step run", latest.Approved, latest.Step, hash)
			}
			if len(latest.Cells) != len(run.Cells) {
				t.Fatalf("cells %+v, want the %d that ran", latest.Cells, len(run.Cells))
			}
			for _, want := range run.Cells {
				k := slices.IndexFunc(latest.Cells, func(c calibration.Cell) bool { return c.ID == want.ID })
				if k < 0 || latest.Cells[k].State != calibration.CellDone || !latest.Cells[k].Passed || latest.Cells[k].Entry != want.Entry {
					t.Fatalf("cells %+v, want %s kept done", latest.Cells, want.ID)
				}
			}
			if latest.Spend.WeeklyPct["claude"] != 1 || latest.Spend.WeeklyPct["codex"] != 1 {
				t.Fatalf("spend %+v, want it kept", latest.Spend.WeeklyPct)
			}
			if out, err := executeBusCommand(t, "calibrate", "judge"); err != nil {
				t.Fatal(out, err)
			}
			if *calls == 0 {
				t.Fatal("no judge call under the new approval")
			}
		})
	}
}

// A plan with a Codex judge readies the judge slot with the project's hooks
// and names it, so the person can trust it in Codex before the run.
func TestCalibratePlanReadiesJudgeSlot(t *testing.T) {
	root := learningRepo(t, true)
	tempCache(t)
	fakeQuota(t)
	hooks := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex", "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}
	minedRun(t, filepath.Join(root, ".duolog"))
	// With measured rates the claude challenger fits the cap, so Codex judges
	// its pairs with the codex entries.
	rates := map[string]calibration.Rate{"codex": {WeeklyPerMTok: 0.5, WindowPerMTok: 1}, "claude": {WeeklyPerMTok: 0.5, WindowPerMTok: 1}}
	if err := calibration.WriteRates(filepath.Join(root, ".duolog"), rates); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "calibrate", "plan")
	if err != nil {
		t.Fatal(out, err)
	}
	slot, err := calibration.JudgeSlot(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "judge folder: "+slot+" (open Codex in this folder once, trust it and approve its hooks in /hooks") {
		t.Fatalf("output does not name the judge slot %s:\n%s", slot, out)
	}
	data, err := os.ReadFile(filepath.Join(slot, ".codex", "hooks.json"))
	if err != nil || string(data) != hooks {
		t.Fatalf("judge slot hooks %q, %v; want the project's", data, err)
	}
}

// Showing the plan again after cells ran keeps them and the approval; the
// new plan is approved on top, keeping the done cells, and the plan already
// approved cannot be approved twice.
func TestCalibratePlanAgainKeepsDoneCells(t *testing.T) {
	root := learningRepo(t, true)
	fakeQuota(t)
	stateDir := filepath.Join(root, ".duolog")
	run := approvedRun(t, stateDir)
	approved := run.Approved.Hash
	for i := range 2 {
		run.Cells[i].State, run.Cells[i].Passed, run.Cells[i].Tokens = calibration.CellDone, true, 600000
	}
	run.Spend.WeeklyPct = map[string]float64{"codex": 2}
	run.Stopped = "re-approve"
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	done := []calibration.Cell{run.Cells[0], run.Cells[1]}
	// New rates change the plan and so its hash.
	if err := calibration.WriteRates(stateDir, map[string]calibration.Rate{"codex": {WeeklyPerMTok: 0.5, WindowPerMTok: 1}}); err != nil {
		t.Fatal(err)
	}

	_, err := executeBusCommand(t, "calibrate", "plan", "--approve", approved)
	if err == nil || !strings.Contains(err.Error(), "already approved") {
		t.Fatalf("err = %v, want the approved plan refused", err)
	}
	out, err := executeBusCommand(t, "calibrate", "plan")
	if err != nil {
		t.Fatal(out, err)
	}
	const prefix = "approve with: duolog calibrate plan --approve "
	i := strings.Index(out, prefix)
	if i < 0 {
		t.Fatalf("output lacks %q:\n%s", prefix, out)
	}
	hash := strings.TrimSpace(strings.SplitN(out[i+len(prefix):], "\n", 2)[0])
	if hash == approved {
		t.Fatal("the new rates kept the plan's hash")
	}
	shown, _, err := calibration.LatestRun(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if shown.Approved == nil || shown.Approved.Hash != approved || len(shown.Cells) != len(run.Cells) || shown.Step != "plan" {
		t.Fatalf("showing the plan changed the run: %+v", shown)
	}
	if p, err := calibration.LoadPlan(run.Dir(stateDir)); err != nil || p.Hash != approved {
		t.Fatalf("plan.json %s, %v; want the approved plan %s", p.Hash, err, approved)
	}

	if out, err := executeBusCommand(t, "calibrate", "plan", "--approve", hash); err != nil {
		t.Fatal(out, err)
	}
	latest, _, err := calibration.LatestRun(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Approved == nil || latest.Approved.Hash != hash || latest.Stopped != "" {
		t.Fatalf("approval %+v, stopped %q; want %s and no stop", latest.Approved, latest.Stopped, hash)
	}
	for _, want := range done {
		found := false
		for _, c := range latest.Cells {
			if c.ID == want.ID {
				found = true
				if c.State != calibration.CellDone || c.Tokens != want.Tokens || c.Task != want.Task || c.Entry != want.Entry {
					t.Fatalf("cell %s = %+v, want it kept done", want.ID, c)
				}
			}
		}
		if !found {
			t.Fatalf("done cell %s is gone", want.ID)
		}
	}
	if latest.Spend.WeeklyPct["codex"] != 2 {
		t.Fatalf("spend %+v, want it kept", latest.Spend)
	}
	if p, err := calibration.LoadPlan(run.Dir(stateDir)); err != nil || p.Hash != hash {
		t.Fatalf("plan.json %s, %v; want the new plan %s", p.Hash, err, hash)
	}
}

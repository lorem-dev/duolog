package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testbin"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/calibration"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/quota"
)

// fakeSurveyChecks keeps the survey from running a real build or test.
func fakeSurveyChecks(t *testing.T) {
	t.Helper()
	old := surveyChecks
	surveyChecks = func(_ context.Context, dir string, commands []string) []journal.Check {
		checks := make([]journal.Check, 0, len(commands))
		for _, command := range commands {
			checks = append(checks, journal.Check{Command: command, Seconds: 1})
		}
		return checks
	}
	t.Cleanup(func() { surveyChecks = old })
}

func TestCalibrateRefusesWorkers(t *testing.T) {
	learningRepo(t, true)
	fakeSurveyChecks(t)
	t.Setenv("DUOLOG_DEPTH", "1")
	subs := newCalibrateCommand().Commands()
	if len(subs) == 0 {
		t.Fatal("calibrate has no subcommands")
	}
	for _, sub := range subs {
		_, err := executeBusCommand(t, "calibrate", sub.Name())
		if err == nil || !strings.Contains(err.Error(), "workers cannot run duolog calibrate") {
			t.Fatalf("calibrate %s err = %v, want workers cannot run duolog calibrate", sub.Name(), err)
		}
	}
}

func TestCalibrateSurveyStartsRun(t *testing.T) {
	root := learningRepo(t, true)
	fakeSurveyChecks(t)
	out, err := executeBusCommand(t, "calibrate", "survey")
	if err != nil {
		t.Fatal(out, err)
	}
	if !strings.Contains(out, "survey:") || !strings.Contains(out, "next: duolog calibrate classes") {
		t.Fatalf("output = %q", out)
	}
	stateDir := filepath.Join(root, ".duolog")
	run, ok, err := calibration.LatestRun(stateDir)
	if err != nil || !ok {
		t.Fatalf("LatestRun = %v, %v", ok, err)
	}
	if run.Step != "survey" {
		t.Fatalf("Step = %q, want survey", run.Step)
	}
	if _, err := os.Stat(filepath.Join(run.Dir(stateDir), "survey.json")); err != nil {
		t.Fatal(err)
	}
}

func TestCalibrateSurveyRefusesUnfinishedRun(t *testing.T) {
	root := learningRepo(t, true)
	fakeSurveyChecks(t)
	if out, err := executeBusCommand(t, "calibrate", "survey"); err != nil {
		t.Fatal(out, err)
	}
	first, _, err := calibration.LatestRun(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = executeBusCommand(t, "calibrate", "survey")
	if err == nil || !strings.Contains(err.Error(), first.ID) || !strings.Contains(err.Error(), "--new") {
		t.Fatalf("second survey err = %v, want a refusal naming %s and --new", err, first.ID)
	}
	if out, err := executeBusCommand(t, "calibrate", "survey", "--new"); err != nil {
		t.Fatal(out, err)
	}
	second, _, err := calibration.LatestRun(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("survey --new did not start a new run")
	}
}

func TestCalibrateSurveyNeedsGit240(t *testing.T) {
	root := learningRepo(t, true)
	fakeSurveyChecks(t)
	testbin.Install(t, "git", testbin.Scenario{Stdout: "git version 2.39.3 (Apple Git-146)\n"})
	_, err := executeBusCommand(t, "calibrate", "survey")
	if err == nil || !strings.Contains(err.Error(), "calibration needs Git 2.40 or newer") || !strings.Contains(err.Error(), "2.39.3") {
		t.Fatalf("survey err = %v, want calibration needs Git 2.40 or newer, naming 2.39.3", err)
	}
	if _, ok, err := calibration.LatestRun(filepath.Join(root, ".duolog")); err != nil || ok {
		t.Fatalf("LatestRun = %v, %v; want no run after the Git check", ok, err)
	}
}

// fakeMineChecks fails a tree that holds a test file without the code file
// beside it, as a hidden test does in the parent export, and passes the rest.
func fakeMineChecks(t *testing.T) {
	t.Helper()
	oldChecks, oldGH := mineChecks, mineGH
	mineChecks = func(_ context.Context, dir string, commands []string) []journal.Check {
		exit := 0
		_ = filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
			if err == nil && strings.HasSuffix(name, "_test.go") {
				if _, err := os.Stat(strings.TrimSuffix(name, "_test.go") + ".go"); err != nil {
					exit = 1
				}
			}
			return nil
		})
		checks := make([]journal.Check, 0, len(commands))
		for _, command := range commands {
			checks = append(checks, journal.Check{Command: command, Exit: exit, Seconds: 1})
		}
		return checks
	}
	mineGH = nil
	t.Cleanup(func() { mineChecks, mineGH = oldChecks, oldGH })
}

func TestCalibrateMinePrintsCounts(t *testing.T) {
	root := learningRepo(t, true)
	fakeMineChecks(t)
	gitIn := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", root, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	for i := range 9 {
		dir := filepath.Join(root, fmt.Sprintf("pkg%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		code := fmt.Sprintf("package pkg%d\n\n// Value%d returns its number.\nfunc Value%d() int {\n\treturn %d\n}\n", i, i, i, i)
		test := fmt.Sprintf("package pkg%d\n\nimport \"testing\"\n\nfunc TestValue%d(t *testing.T) {\n\tif Value%d() != %d {\n\t\tt.Fatal(\"value\")\n\t}\n}\n", i, i, i, i)
		if err := os.WriteFile(filepath.Join(dir, "value.go"), []byte(code), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "value_test.go"), []byte(test), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn("add", fmt.Sprintf("pkg%d", i))
		gitIn("commit", "-q", "-m", fmt.Sprintf("add value %d", i))
	}

	stateDir := filepath.Join(root, ".duolog")
	run, err := calibration.NewRun(stateDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	survey := calibration.Survey{Files: 500, Commits: 300, HistoryDays: 300, CheckSeconds: 2,
		Checks: map[string][]string{"implement": {"go test ./..."}}}
	if err := calibration.SaveSurvey(stateDir, run.ID, survey); err != nil {
		t.Fatal(err)
	}
	run.Step = "classes"
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}

	out, err := executeBusCommand(t, "calibrate", "mine")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"class implement: 9 candidates, 6 selected, 3 reserve", "excluded: 1 no parent", "next: duolog calibrate plan"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(run.Dir(stateDir), "manifest.json")); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := calibration.LatestRun(stateDir)
	if err != nil || !ok {
		t.Fatalf("LatestRun = %v, %v", ok, err)
	}
	if latest.Step != "mine" {
		t.Fatalf("Step = %q, want mine", latest.Step)
	}
}

func TestCalibrateStatusWithoutRun(t *testing.T) {
	learningRepo(t, true)
	out, err := executeBusCommand(t, "calibrate", "status")
	if err != nil {
		t.Fatal(out, err)
	}
	if strings.TrimSpace(out) != "no calibration run" {
		t.Fatalf("output = %q, want no calibration run", out)
	}
}

func TestCalibrateStatusNamesIgnoredSharedCap(t *testing.T) {
	root := learningRepo(t, true)
	fakeSurveyChecks(t)
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("learning:\n  enabled: true\ncalibration:\n  max_weekly_share: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := executeBusCommand(t, "calibrate", "survey"); err != nil {
		t.Fatal(out, err)
	}
	out, err := executeBusCommand(t, "calibrate", "status")
	if err != nil {
		t.Fatal(out, err)
	}
	if want := "note: calibration.max_weekly_share 1 in .duolog/config.yaml is ignored: a shared config may only lower calibration caps; 0.1 applies"; !strings.Contains(out, want) {
		t.Fatalf("status lacks %q:\n%s", want, out)
	}
}

// calibrationGoRepo is a learning repository with a committed Go module, so
// the survey proposes go vet and go test as checks.
func calibrationGoRepo(t *testing.T) string {
	t.Helper()
	root := learningRepo(t, true)
	for name, body := range map[string]string{
		"go.mod":       "module example.invalid/demo\n\ngo 1.24\n",
		"main.go":      "package main\n\nfunc main() {}\n",
		"main_test.go": "package main\n\nimport \"testing\"\n\nfunc TestMain(t *testing.T) {}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "go.mod", "main.go", "main_test.go"}, {"-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "add module"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	fakeSurveyChecks(t)
	if out, err := executeBusCommand(t, "calibrate", "survey"); err != nil {
		t.Fatal(out, err)
	}
	return root
}

func TestCalibrateClassesShowsDiffAndStops(t *testing.T) {
	root := calibrationGoRepo(t)
	path := filepath.Join(root, ".duolog", "config.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "calibrate", "classes")
	if err != nil {
		t.Fatal(out, err)
	}
	if !strings.Contains(out, "proposed .duolog/config.yaml:") || !strings.Contains(out, "apply with: duolog calibrate classes --apply") {
		t.Fatalf("output = %q", out)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("config.yaml changed without --apply: %q, %v", after, err)
	}
	run, _, err := calibration.LatestRun(filepath.Join(root, ".duolog"))
	if err != nil || run.Step != "survey" {
		t.Fatalf("LatestRun = %+v, %v; want step survey", run, err)
	}
}

func TestCalibrateClassesApply(t *testing.T) {
	root := calibrationGoRepo(t)
	if out, err := executeBusCommand(t, "calibrate", "classes", "--apply"); err != nil {
		t.Fatal(out, err)
	}
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Learning.Classes["implement"].Checks; !reflect.DeepEqual(got, []string{"go vet ./...", "go test ./..."}) {
		t.Fatalf("Checks[implement] = %v", got)
	}
	run, ok, err := calibration.LatestRun(filepath.Join(root, ".duolog"))
	if err != nil || !ok || run.Step != "classes" {
		t.Fatalf("LatestRun = %+v, %v, %v; want step classes", run, ok, err)
	}
}

func TestCalibrateClassesNeedsSurvey(t *testing.T) {
	learningRepo(t, true)
	_, err := executeBusCommand(t, "calibrate", "classes")
	if err == nil || !strings.Contains(err.Error(), "run duolog calibrate survey first") {
		t.Fatalf("err = %v, want run duolog calibrate survey first", err)
	}
}

// fakeQuota gives every agent a weekly and a five-hour window at 20% used.
func fakeQuota(t *testing.T) {
	t.Helper()
	old := quotaReader
	quotaReader = func(ctx context.Context, agent string) ([]calibration.Window, error) {
		reset := time.Now().Add(time.Hour)
		return []calibration.Window{
			{Kind: calibration.KindWeekly, UsedPercent: 20, ResetsAt: reset.Add(72 * time.Hour)},
			{Kind: calibration.KindFiveHour, UsedPercent: 20, ResetsAt: reset},
		}, nil
	}
	t.Cleanup(func() { quotaReader = old })
}

// minedRun saves a run at the mine step with three tests tasks.
func minedRun(t *testing.T, stateDir string) *calibration.Run {
	t.Helper()
	run, err := calibration.NewRun(stateDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m := calibration.Manifest{Counts: map[string]calibration.ClassCount{"tests": {Candidates: 3, Valid: 3, Selected: 3}},
		Estimates: map[string]calibration.Estimate{}, RepoFiles: 300, HistoryDays: 200, CheckSeconds: 2}
	for i := range 3 {
		id := fmt.Sprintf("t%d", i+1)
		m.Tasks = append(m.Tasks, calibration.ManifestTask{ID: id, Class: "tests", Statement: "Add tests for value " + id + ".",
			Verification: "hidden_tests", Features: map[string]string{"output": "tests", "scope": "1-3"}})
		m.Estimates[id] = calibration.Estimate{Files: 1, Lines: 30, TokensLow: 80000, TokensHigh: 250000, Minutes: 8, CheckSeconds: 2}
	}
	if err := calibration.SaveManifest(run.Dir(stateDir), m); err != nil {
		t.Fatal(err)
	}
	run.Step = "mine"
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestCalibratePlanSavesAndApproves(t *testing.T) {
	root := learningRepo(t, true)
	fakeQuota(t)
	stateDir := filepath.Join(root, ".duolog")
	run := minedRun(t, stateDir)

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
	if _, err := os.Stat(filepath.Join(run.Dir(stateDir), "plan.json")); err != nil {
		t.Fatal(err)
	}

	_, err = executeBusCommand(t, "calibrate", "plan", "--approve", "0000")
	if err == nil || !strings.Contains(err.Error(), "plan hash does not match") {
		t.Fatalf("wrong hash err = %v, want plan hash does not match", err)
	}
	if out, err := executeBusCommand(t, "calibrate", "plan", "--approve", hash); err != nil {
		t.Fatal(out, err)
	}
	latest, ok, err := calibration.LatestRun(stateDir)
	if err != nil || !ok {
		t.Fatalf("LatestRun = %v, %v", ok, err)
	}
	if latest.Approved == nil || latest.Approved.Hash != hash {
		t.Fatalf("Approved = %+v, want hash %s", latest.Approved, hash)
	}
	if latest.Step != "plan" {
		t.Fatalf("Step = %q, want plan", latest.Step)
	}
	// tests: 2 codex entries and the claude challenger, 3 tasks. Without a
	// rate each cell counts 1% weekly and a judge call the bound scaled by its
	// tokens, so the challenger fits the cap.
	if len(latest.Cells) != 9 {
		t.Fatalf("len(Cells) = %d, want 9", len(latest.Cells))
	}
	for _, cell := range latest.Cells {
		if cell.State != calibration.CellPending {
			t.Fatalf("cell %s state = %q, want pending", cell.ID, cell.State)
		}
	}
}

func TestCalibratePlanNamesCodexTree(t *testing.T) {
	root := learningRepo(t, true)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)
	fakeQuota(t)
	minedRun(t, filepath.Join(root, ".duolog"))
	// Installed at project scope only, not committed yet.
	hooks := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex", "hooks.json"), []byte(hooks), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := executeBusCommand(t, "calibrate", "plan")
	if err != nil {
		t.Fatal(out, err)
	}
	tree, err := calibration.TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "tree: " + tree + " (open Codex in this folder once, trust it and approve its hooks in /hooks; duolog never edits Codex config)\n"
	if strings.Count(out, "tree: ") != 1 || !strings.Contains(out, want) {
		t.Fatalf("output lacks one line %q:\n%s", want, out)
	}
	// The hooks to approve are already in the folder.
	if body, err := os.ReadFile(filepath.Join(tree, ".codex", "hooks.json")); err != nil || string(body) != hooks {
		t.Fatalf("hooks.json in %s = %q, %v; want the project's", tree, body, err)
	}
}

func TestWritePlanTreeWithoutCodex(t *testing.T) {
	isolatedRouteConfig(t)
	p := calibration.Plan{Classes: []calibration.ClassPlan{{Class: "docs", Entries: []calibration.PlanEntry{{Key: "claude/sonnet/low"}}}}}
	var b strings.Builder
	if err := writePlanTree(context.Background(), &b, t.TempDir(), p); err != nil {
		t.Fatal(err)
	}
	if b.Len() != 0 {
		t.Fatalf("output = %q, want nothing for a plan without Codex", b.String())
	}
}

// approvedRun saves a mined run and approves its plan.
func approvedRun(t *testing.T, stateDir string) *calibration.Run {
	t.Helper()
	minedRun(t, stateDir)
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
	if out, err := executeBusCommand(t, "calibrate", "plan", "--approve", hash); err != nil {
		t.Fatal(out, err)
	}
	run, _, err := calibration.LatestRun(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestCalibrateResumeWaitsForPause(t *testing.T) {
	root := learningRepo(t, true)
	fakeQuota(t)
	stateDir := filepath.Join(root, ".duolog")
	run := approvedRun(t, stateDir)
	pause := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	run.Pause = pause
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "calibrate", "resume")
	if err != nil {
		t.Fatal(out, err)
	}
	if want := "paused until " + pause.Format(time.RFC3339); !strings.Contains(out, want) {
		t.Fatalf("output lacks %q:\n%s", want, out)
	}
	latest, _, err := calibration.LatestRun(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range latest.Cells {
		if c.State != calibration.CellPending {
			t.Fatalf("cell %+v ran before the pause ended", c)
		}
	}
	status, err := executeBusCommand(t, "calibrate", "status")
	if err != nil {
		t.Fatal(status, err)
	}
	for _, want := range []string{"done 0, pending 9, running 0", "paused until " + pause.Format(time.RFC3339), "next: duolog calibrate resume"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status lacks %q:\n%s", want, status)
		}
	}
}

func TestCalibrateRunKeepsApprovedLimits(t *testing.T) {
	// Approved with a 10% weekly cap and 30-minute cells; the person's config
	// says 20% and 60 minutes afterwards. The run and its cells keep to the
	// approval until a new plan is approved.
	root := learningRepo(t, true)
	fakeQuota(t)
	stateDir := filepath.Join(root, ".duolog")
	if err := os.WriteFile(filepath.Join(stateDir, "config.local.yaml"), []byte("calibration: {max_weekly_share: 0.1, cell_max_minutes: 30}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if run := approvedRun(t, stateDir); len(run.Cells) == 0 {
		t.Fatal("the approved plan has no cells; the run would have nothing to keep to its limits")
	}
	if err := os.WriteFile(filepath.Join(stateDir, "config.local.yaml"), []byte("calibration: {max_weekly_share: 0.2, cell_max_minutes: 60, cell_max_turns: 20}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var got config.Calibration
	err := withCalibrationRunner(newCalibrateRunCommand(), "", func(r *calibration.Runner) error {
		got = r.Cfg.Calibration
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWeeklyShare != 0.1 || got.CellMaxMinutes != 30 || got.CellMaxTurns != 20 {
		t.Fatalf("run limits %+v, want 0.1 weekly and 30 minutes from the approval, 20 turns from the stricter config", got)
	}
}

// A key that env_passthrough lists no longer reaches a free worker: every
// cell, judge call and rewrite launches through workerenv.Build, whose free
// variant drops it unless paid_runs.<agent>.allow is true, and a paid route
// needs paid_runs.<agent>.calibrate under the approved plan's budget. So a
// listed key no longer stops calibration before it starts; the billing gate
// decides each step.
func TestCalibrateListedKeyLeavesBillingToTheGate(t *testing.T) {
	root := learningRepo(t, true)
	fakeQuota(t)
	stateDir := filepath.Join(root, ".duolog")
	if run := approvedRun(t, stateDir); len(run.Cells) == 0 {
		t.Fatal("the approved plan has no cells")
	}
	local := filepath.Join(stateDir, "config.local.yaml")
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		if err := os.WriteFile(local, []byte("env_passthrough: [PATH, HOME, USER, CODEX_HOME, CLAUDE_CONFIG_DIR, "+key+"]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		called := false
		if err := withCalibrationRunner(newCalibrateRunCommand(), "", func(*calibration.Runner) error {
			called = true
			return nil
		}); err != nil || !called {
			t.Fatalf("%s listed: runner called %t, err %v", key, called, err)
		}
	}
}

func TestCalibrateFirstApprovalOfEmptyPlanRuns(t *testing.T) {
	// A 5% cap cuts every cell. The first approval still leads to
	// calibrate run, not past it to judge.
	root := learningRepo(t, true)
	fakeQuota(t)
	stateDir := filepath.Join(root, ".duolog")
	if err := os.WriteFile(filepath.Join(stateDir, "config.local.yaml"), []byte("calibration: {max_weekly_share: 0.05}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := approvedRun(t, stateDir)
	if len(run.Cells) != 0 || run.Step != "plan" {
		t.Fatalf("step %s with %d cells, want step plan with no cells", run.Step, len(run.Cells))
	}
	if err := withCalibrationRunner(newCalibrateRunCommand(), "", func(*calibration.Runner) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaWindowsKeepModelLimits(t *testing.T) {
	reset := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	s := quota.Snapshot{
		Windows: []quota.Window{
			{Kind: "session", DurationMinutes: 300, UsedPercent: 10, ResetsAt: reset},
			{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 20, ResetsAt: reset.Add(72 * time.Hour)},
		},
		ModelLimits: map[string][]quota.Window{"sol": {{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 84, ResetsAt: reset.Add(48 * time.Hour)}}},
	}
	want := []calibration.Window{
		{Kind: calibration.KindFiveHour, UsedPercent: 10, ResetsAt: reset},
		{Kind: calibration.KindWeekly, UsedPercent: 20, ResetsAt: reset.Add(72 * time.Hour)},
		{Kind: calibration.KindWeekly, Model: "sol", UsedPercent: 84, ResetsAt: reset.Add(48 * time.Hour)},
	}
	if got := quotaWindows(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("quotaWindows = %+v, want %+v", got, want)
	}
}

// A family the account refuses keeps its windows below 100 percent, so the
// calibration window counts as used up and admits no cell of that family.
func TestQuotaWindowsTreatRejectedFamilyAsFull(t *testing.T) {
	reset := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	s := quota.Snapshot{
		Windows: []quota.Window{{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 20, ResetsAt: reset}},
		ModelLimits: map[string][]quota.Window{"sol": {
			{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 30, ResetsAt: reset, Severity: "rejected"},
		}},
	}
	want := []calibration.Window{
		{Kind: calibration.KindWeekly, UsedPercent: 20, ResetsAt: reset},
		{Kind: calibration.KindWeekly, Model: "sol", UsedPercent: 100, ResetsAt: reset},
	}
	if got := quotaWindows(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("quotaWindows = %+v, want %+v", got, want)
	}
}

func TestCalibrateResumeNeedsApproval(t *testing.T) {
	root := learningRepo(t, true)
	fakeQuota(t)
	minedRun(t, filepath.Join(root, ".duolog"))
	_, err := executeBusCommand(t, "calibrate", "resume")
	if err == nil || !strings.Contains(err.Error(), "approve the plan first") {
		t.Fatalf("err = %v, want approve the plan first", err)
	}
}

func TestCalibratePlanNeedsMine(t *testing.T) {
	learningRepo(t, true)
	fakeSurveyChecks(t)
	fakeQuota(t)
	if out, err := executeBusCommand(t, "calibrate", "survey"); err != nil {
		t.Fatal(out, err)
	}
	_, err := executeBusCommand(t, "calibrate", "plan")
	if err == nil || !strings.Contains(err.Error(), "run duolog calibrate mine first") {
		t.Fatalf("err = %v, want run duolog calibrate mine first", err)
	}
}

func TestCalibrateRunNeedsApproval(t *testing.T) {
	root := learningRepo(t, true)
	fakeQuota(t)
	minedRun(t, filepath.Join(root, ".duolog"))
	_, err := executeBusCommand(t, "calibrate", "run")
	if err == nil || !strings.Contains(err.Error(), "approve the plan first") {
		t.Fatalf("err = %v, want approve the plan first", err)
	}
}

func TestCalibrateStatusShowsRun(t *testing.T) {
	learningRepo(t, true)
	fakeSurveyChecks(t)
	if out, err := executeBusCommand(t, "calibrate", "survey"); err != nil {
		t.Fatal(out, err)
	}
	out, err := executeBusCommand(t, "calibrate", "status")
	if err != nil {
		t.Fatal(out, err)
	}
	if !strings.Contains(out, "step: survey") || !strings.Contains(out, "next: duolog calibrate classes") {
		t.Fatalf("output = %q", out)
	}
}

// fakeJudge scores the claude cells' patches (c-1, c-3 in ranRun) above the
// codex ones, whichever order the judge sees them in.
func fakeJudge(t *testing.T) *int {
	t.Helper()
	// Judge calls are admitted against quota and run in a slot Codex trusts.
	fakeQuota(t)
	tempCache(t)
	trustJudgeSlot(t)
	calls := 0
	old := runDelegate
	runDelegate = func(ctx context.Context, _ *config.Config, _ *store.Store, _, _, _ string) (delegate.Outcome, error) {
		calls++
		first, err := os.ReadFile(filepath.Join(delegate.OptionsFrom(ctx).WorkRoot, "a.patch"))
		if err != nil {
			t.Fatal(err)
		}
		a1, b1, a2, b2, ca, cb := 3, 1, 3, 2, 0, 1
		if !strings.Contains(string(first), "c-1") && !strings.Contains(string(first), "c-3") {
			a1, b1, a2, b2, ca, cb = b1, a1, b2, a2, cb, ca
		}
		summary := fmt.Sprintf(`{"scores":[{"criterion":"correct against the tests and the brief","a":%d,"b":%d,"reason":"tests"},`+
			`{"criterion":"false claims (count)","a":%d,"b":%d,"count_a":%d,"count_b":%d,"reason":"claims"}]}`, a1, b1, a2, b2, ca, cb)
		return delegate.Outcome{Result: runner.Result{Done: true, Summary: summary}}, nil
	}
	t.Cleanup(func() { runDelegate = old })
	return &calls
}

// ranRun saves a run at the run step with two implement tasks, one of them
// rewritten, each passed by two entries.
func ranRun(t *testing.T, stateDir string) *calibration.Run {
	t.Helper()
	run, err := calibration.NewRun(stateDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runDir := run.Dir(stateDir)
	m := calibration.Manifest{Rewrite: []string{"t1"}}
	for i, id := range []string{"t1", "t2"} {
		task := calibration.TaskRecord{ManifestTask: calibration.ManifestTask{ID: id, Class: "implement", Verification: "hidden",
			Statement: "Add a function Value" + id + " that returns its number.", Features: map[string]string{"output": "code"}}}
		m.Tasks = append(m.Tasks, task.ManifestTask)
		data, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			filepath.Join(calibration.TasksDir, id, "task.json"):       string(data),
			filepath.Join(calibration.TasksDir, id, "statement.txt"):   task.Statement + "\n",
			filepath.Join(calibration.TasksDir, id, "reference.patch"): "+reference\n",
		}
		for j, entry := range []string{"claude/opus/high", "codex/sol/high"} {
			cell := fmt.Sprintf("c-%d", 2*i+j+1)
			run.Cells = append(run.Cells, calibration.Cell{ID: cell, Task: id, Class: "implement", Entry: entry, Round: 1, State: calibration.CellDone, Passed: true})
			files[filepath.Join(calibration.CellsDir, cell, "result.patch")] = "+change " + cell + "\n"
			files[filepath.Join(calibration.CellsDir, cell, "checks.json")] = `{"hidden":[{"command":"go test ./...","exit":0,"seconds":1}]}`
		}
		for name, body := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(runDir, name)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(runDir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := calibration.SaveManifest(runDir, m); err != nil {
		t.Fatal(err)
	}
	run.Step = "run"
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestCalibrateJudgePrintsBatchPerClass(t *testing.T) {
	root := learningRepo(t, true)
	calls := fakeJudge(t)
	stateDir := filepath.Join(root, ".duolog")
	ranRun(t, stateDir)
	out, err := executeBusCommand(t, "calibrate", "judge", "--class", "implement")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"rewritten statements:", "Add a function Valuet1 that returns its number.", "drop with: duolog calibrate drop t1",
		"class implement: 2 verdicts", "A claude/opus/high", "B codex/sol/high", "duolog verdict ", " confirm",
		`correct "correct against the tests and the brief=3/1"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "drop with: duolog calibrate drop t2") {
		t.Fatalf("output offers to drop a task that was not rewritten:\n%s", out)
	}
	if strings.Index(out, "rewritten statements:") > strings.Index(out, "class implement:") {
		t.Fatalf("rewritten statements come after the verdicts:\n%s", out)
	}
	// Cross-vendor pairs, clear results: one call per vendor per task.
	if *calls != 4 {
		t.Fatalf("%d judge calls, want 4", *calls)
	}
	latest, _, err := calibration.LatestRun(stateDir)
	if err != nil || latest.Step != "judge" {
		t.Fatalf("LatestRun = %+v, %v; want step judge", latest, err)
	}
	// A second judge reuses the verdicts and spends nothing.
	again, err := executeBusCommand(t, "calibrate", "judge")
	if err != nil {
		t.Fatal(again, err)
	}
	if *calls != 4 || !strings.Contains(again, "class implement: 2 verdicts") {
		t.Fatalf("%d calls after a second judge:\n%s", *calls, again)
	}
}

// A cell whose candidate changed the check setup is neither judged nor
// counted: judge shows it, with the files and how its checks went, for the
// person to decide.
func TestCalibrateJudgeShowsCheckSetupChanged(t *testing.T) {
	root := learningRepo(t, true)
	calls := fakeJudge(t)
	stateDir := filepath.Join(root, ".duolog")
	run := ranRun(t, stateDir)
	run.Cells[2].State, run.Cells[2].SetupChanged = calibration.CellSetupChanged, []string{"Makefile", "package.json"}
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "calibrate", "judge")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"check setup changed, no pass or failure;",
		"  cell c-3: task t2, claude/opus/high, changed Makefile, package.json; checks passed",
		"  patch: " + filepath.Join(run.Dir(stateDir), calibration.CellsDir, "c-3", "result.patch"),
		"  drop with: duolog calibrate drop t2", "class implement: 1 verdicts"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	// Only t1's pair is judged.
	if *calls != 2 {
		t.Fatalf("%d judge calls, want 2", *calls)
	}
	j := &journal.Journal{Dir: filepath.Join(stateDir, "journal")}
	cs, err := journal.ReadAll[journal.CalibrationResult](j, journal.CalibrationFile)
	if err != nil || len(cs) != 3 || slices.ContainsFunc(cs, func(c journal.CalibrationResult) bool { return c.Task == "t2" && c.Entry == "claude/opus/high" }) {
		t.Fatalf("results %+v, %v; want three, none for c-3", cs, err)
	}
}

func TestCalibrateDrop(t *testing.T) {
	root := learningRepo(t, true)
	stateDir := filepath.Join(root, ".duolog")
	run := ranRun(t, stateDir)
	if out, err := executeBusCommand(t, "calibrate", "drop", "t1"); err != nil {
		t.Fatal(out, err)
	}
	_, err := executeBusCommand(t, "calibrate", "drop", "t9")
	if err == nil || !strings.Contains(err.Error(), "task t9 is not in the run") {
		t.Fatalf("err = %v, want task t9 is not in the run", err)
	}
	j := &journal.Journal{Dir: filepath.Join(stateDir, "journal")}
	cs, err := journal.ReadAll[journal.CalibrationResult](j, journal.CalibrationFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Kind != "drop" || cs[0].Run != run.ID || cs[0].Task != "t1" || cs[0].Source != "calibration" {
		t.Fatalf("records %+v, want one drop of t1", cs)
	}
}

func TestCalibrateJudgeRecordsResults(t *testing.T) {
	root := learningRepo(t, true)
	fakeJudge(t)
	stateDir := filepath.Join(root, ".duolog")
	ranRun(t, stateDir)
	if out, err := executeBusCommand(t, "calibrate", "judge"); err != nil {
		t.Fatal(out, err)
	}
	j := &journal.Journal{Dir: filepath.Join(stateDir, "journal")}
	cs, err := journal.ReadAll[journal.CalibrationResult](j, journal.CalibrationFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 4 {
		t.Fatalf("%d results, want one per done cell: %+v", len(cs), cs)
	}
}

// judgedRun saves a run at the judge step with its survey and manifest, and
// journals its check results: sol passes three tests tasks that terra fails,
// and terra passes two docs tasks that sonnet fails, too few to separate.
func judgedRun(t *testing.T, root string) *calibration.Run {
	t.Helper()
	stateDir := filepath.Join(root, ".duolog")
	run, err := calibration.NewRun(stateDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	survey := calibration.Survey{Facts: calibration.SurveyFacts{Languages: []string{"go"}, Build: []string{"go build ./..."},
		Test: []string{"go vet ./...", "go test ./..."}, Components: []string{"cmd"}}, Files: 40, Commits: 30, CheckSeconds: 2}
	if err := calibration.SaveSurvey(stateDir, run.ID, survey); err != nil {
		t.Fatal(err)
	}
	j, err := journal.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	m := calibration.Manifest{}
	add := func(id, class, output, entry string, passed bool) {
		if !slices.ContainsFunc(m.Tasks, func(task calibration.ManifestTask) bool { return task.ID == id }) {
			m.Tasks = append(m.Tasks, calibration.ManifestTask{ID: id, Class: class, Statement: "Change " + id + ".", StatementSource: "commit",
				Verification: "hidden", Features: map[string]string{"output": output}})
		}
		run.Cells = append(run.Cells, calibration.Cell{ID: id + "-" + entry, Task: id, Class: class, Entry: entry, Round: 1, State: calibration.CellDone, Passed: passed})
		c := journal.CalibrationResult{Kind: "result", Time: now, Run: run.ID, Task: id, Class: class, Entry: entry, Verification: "hidden", Passed: passed, Source: "calibration"}
		if err := j.Append(journal.CalibrationFile, c); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		id := fmt.Sprintf("t%d", i+1)
		add(id, "tests", "tests", "codex/terra/medium", false)
		add(id, "tests", "tests", "codex/sol/medium", true)
	}
	for i := range 2 {
		id := fmt.Sprintf("d%d", i+1)
		add(id, "docs", "docs", "claude/sonnet/medium", false)
		add(id, "docs", "docs", "codex/terra/medium", true)
	}
	// A task held for round 2 that never ran stays out of the project part.
	m.Tasks = append(m.Tasks, calibration.ManifestTask{ID: "t9", Class: "tests", Statement: "Change t9.", StatementSource: "commit", Verification: "hidden"})
	if err := calibration.SaveManifest(run.Dir(stateDir), m); err != nil {
		t.Fatal(err)
	}
	run.Step = "judge"
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	return run
}

// sharedProjectPart writes a project part in which sol passed three tests
// tasks that terra failed.
func sharedProjectPart(t *testing.T, root string) []byte {
	t.Helper()
	p := calibration.ProjectPart{Version: 1, Evidence: map[string][]calibration.EvidenceRow{"tests": {
		{Agent: "codex", Family: "sol", Effort: "medium", Tasks: 3, TasksPassed: 3, Evidence: 3, Date: "2026-10-01", DuologVersion: "1.2.0"},
		{Agent: "codex", Family: "terra", Effort: "medium", Tasks: 3, Evidence: 3, Date: "2026-10-01", DuologVersion: "1.2.0"},
	}}}
	_, data, err := calibration.WriteProject(filepath.Join(root, ".duolog"), p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCalibrateProposeShowsBothDiffs(t *testing.T) {
	root := learningRepo(t, true)
	judgedRun(t, root)
	before := sharedProjectPart(t, root)
	project := filepath.Join(root, ".duolog", "calibration", "project.yaml")
	learned := filepath.Join(root, ".duolog", config.LearnedRoutingFile)

	out, err := executeBusCommand(t, "calibrate", "propose")
	if err != nil {
		t.Fatal(out, err)
	}
	last := -1
	for _, want := range []string{
		"proposed .duolog/routing.learned.yaml:",
		"class docs: no difference found, keeps the configured order",
		"proposed .duolog/calibration/project.yaml:",
		"apply the order with: duolog learn --apply",
		"apply the project part with: duolog calibrate propose --apply-project",
	} {
		i := strings.Index(out, want)
		if i < 0 {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
		if i < last {
			t.Fatalf("%q is out of order:\n%s", want, out)
		}
		last = i
	}
	if strings.Contains(out, "seeded from the shared project part") {
		t.Fatalf("personal results are seeded from the project part:\n%s", out)
	}
	if _, err := os.Stat(learned); !os.IsNotExist(err) {
		t.Fatalf("propose wrote %s: %v", learned, err)
	}
	after, err := os.ReadFile(project)
	if err != nil || string(after) != string(before) {
		t.Fatalf("propose changed project.yaml: %v\n%s", err, after)
	}
}

// A calibrated order routes nothing while learning mode is off; propose says
// so in one line, and says nothing when learning mode is on.
func TestCalibrateProposeSaysLearningIsOff(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		root := learningRepo(t, enabled)
		judgedRun(t, root)
		out, err := executeBusCommand(t, "calibrate", "propose")
		if err != nil || !strings.Contains(out, "proposed .duolog/routing.learned.yaml:") {
			t.Fatal(enabled, out, err)
		}
		if got := strings.Count(out, learningOffLine); enabled && got != 0 || !enabled && got != 1 {
			t.Fatalf("learning enabled %t: %d off lines, output:\n%s", enabled, got, out)
		}
	}
}

func TestCalibrateProposeApplyProject(t *testing.T) {
	root := learningRepo(t, true)
	run := judgedRun(t, root)
	stateDir := filepath.Join(root, ".duolog")
	out, err := executeBusCommand(t, "calibrate", "propose", "--apply-project")
	if err != nil {
		t.Fatal(out, err)
	}
	if !strings.Contains(out, "updated .duolog/calibration/project.yaml") {
		t.Fatalf("output = %q", out)
	}
	p, ok, err := calibration.ReadProject(stateDir)
	if err != nil || !ok {
		t.Fatalf("ReadProject = %v, %v", ok, err)
	}
	var ids []string
	for _, task := range p.Tasks {
		ids = append(ids, task.ID)
	}
	if want := []string{"t1", "t2", "t3", "d1", "d2"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("tasks = %v, want %v", ids, want)
	}
	if len(p.Evidence["tests"]) != 2 || len(p.Evidence["docs"]) != 2 {
		t.Fatalf("evidence = %+v", p.Evidence)
	}
	path := filepath.Join(stateDir, "calibration", "project.yaml")
	if err := exec.Command("git", "-C", root, "check-ignore", "-q", path).Run(); err == nil {
		t.Fatal("git ignores .duolog/calibration/project.yaml")
	} else if exit := (*exec.ExitError)(nil); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("git check-ignore: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(strings.ToLower(string(data)), "usd") {
		t.Fatalf("project.yaml: %v\n%s", err, data)
	}
	if _, err := os.Stat(filepath.Join(stateDir, config.LearnedRoutingFile)); !os.IsNotExist(err) {
		t.Fatalf("--apply-project wrote the learned order: %v", err)
	}
	latest, _, err := calibration.LatestRun(stateDir)
	if err != nil || latest.ID != run.ID || latest.Step != "done" {
		t.Fatalf("LatestRun = %+v, %v; want %s at step done", latest, err, run.ID)
	}
}

func TestCalibrateProposeFinishesRun(t *testing.T) {
	root := learningRepo(t, true)
	fakeSurveyChecks(t)
	run := judgedRun(t, root)
	stateDir := filepath.Join(root, ".duolog")
	if out, err := executeBusCommand(t, "calibrate", "propose"); err != nil {
		t.Fatal(out, err)
	}
	latest, _, err := calibration.LatestRun(stateDir)
	if err != nil || latest.Step != "done" {
		t.Fatalf("LatestRun = %+v, %v; want step done", latest, err)
	}
	// A finished run may still apply its project part.
	if out, err := executeBusCommand(t, "calibrate", "propose", "--apply-project"); err != nil {
		t.Fatal(out, err)
	}
	if out, err := executeBusCommand(t, "calibrate", "survey"); err != nil {
		t.Fatalf("survey after a finished run: %v\n%s", err, out)
	}
	latest, _, err = calibration.LatestRun(stateDir)
	if err != nil || latest.ID == run.ID {
		t.Fatalf("LatestRun = %+v, %v; want a new run", latest, err)
	}
}

func TestCalibrateProposeNeedsJudge(t *testing.T) {
	root := learningRepo(t, true)
	minedRun(t, filepath.Join(root, ".duolog"))
	_, err := executeBusCommand(t, "calibrate", "propose")
	if err == nil || !strings.Contains(err.Error(), "next: duolog calibrate plan") {
		t.Fatalf("err = %v, want the next step", err)
	}
}

func TestCalibrateProposeReadsProjectPartWithoutRuns(t *testing.T) {
	root := learningRepo(t, true)
	sharedProjectPart(t, root)
	out, err := executeBusCommand(t, "calibrate", "propose")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"seeded from the shared project part", "proposed .duolog/routing.learned.yaml:", "apply the order with: duolog learn --apply"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--apply-project") {
		t.Fatalf("without a run there is no project part to apply:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", "journal", journal.CalibrationFile)); !os.IsNotExist(err) {
		t.Fatalf("propose wrote the calibration journal: %v", err)
	}
	_, err = executeBusCommand(t, "calibrate", "propose", "--apply-project")
	if err == nil || !strings.Contains(err.Error(), "no calibration run") {
		t.Fatalf("--apply-project without a run err = %v", err)
	}
}

func TestVerdictConfirmsCalibrationVerdict(t *testing.T) {
	root := learningRepo(t, true)
	fakeJudge(t)
	stateDir := filepath.Join(root, ".duolog")
	ranRun(t, stateDir)
	if out, err := executeBusCommand(t, "calibrate", "judge"); err != nil {
		t.Fatal(out, err)
	}
	j := &journal.Journal{Dir: filepath.Join(stateDir, "journal")}
	vs, err := journal.ReadAll[journal.Verdict](j, "verdicts.ndjson")
	if err != nil || len(vs) != 2 || vs[0].Source != "calibration" {
		t.Fatalf("verdicts %+v, %v", vs, err)
	}
	if out, err := executeBusCommand(t, "verdict", vs[0].ID, "confirm"); err != nil {
		t.Fatal(out, err)
	}
	vs, err = journal.ReadAll[journal.Verdict](j, "verdicts.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	last := vs[len(vs)-1]
	if len(vs) != 3 || last.Kind != "decision" || last.Decision != "confirm" || last.ID != vs[0].ID {
		t.Fatalf("verdicts %+v, want a confirm decision appended", vs)
	}
}

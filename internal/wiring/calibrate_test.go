package wiring_test

// The calibration tests here run a cell, a run or a judge call on what only
// wiring holds: the real delegation (wiring.DelegateDeps) with the agents'
// workers, and the agents' real tree guards. The tests of internal/app/calibrate
// run the same code on fakes.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	calibration "github.com/lorem-dev/duolog/internal/app/calibrate"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	corerun "github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// calTime is a fixed UTC instant, so run ids and dates do not depend on the
// clock.
var calTime = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

const parseStatement = "Add a function Parse(line string) []string that splits line on commas."

// gateHooks is a Codex hooks.json with the duolog gate on every tool.
const gateHooks = `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`

// parseAnswer is a Claude worker's output after it wrote parse.go.
const parseAnswer = `{"session_id":"worker-1","usage":{"input_tokens":1000,"output_tokens":200},"structured_output":{"done":true,"summary":"Added Parse.","files":["parse.go"],"needs":[]}}` + "\n"

// calConfig loads the default configuration with a temporary home, so no
// layer of the person's own configuration is read.
func calConfig(t *testing.T) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	cfg, err := configfile.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// calHome points the home, the user cache and the Codex config at a fresh
// folder, so no test reads or writes the person's own.
func calHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "cache"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

// trustCodex writes a Codex config under home that trusts dir and approves
// the gate hook in dir/.codex/hooks.json, as Codex does once the person
// trusts the folder and reviews its hooks.
func trustCodex(t *testing.T, home, dir string, folder, hooks bool) {
	t.Helper()
	var s string
	if folder {
		s += fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", dir)
	}
	if hooks {
		s += fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", filepath.Join(dir, ".codex", "hooks.json")+":pre_tool_use:0:0")
	}
	calWrite(t, filepath.Join(home, ".codex"), "config.toml", s)
}

func calGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func calWrite(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// calCommit stages every file in root and returns the new commit.
func calCommit(t *testing.T, root, message string) string {
	t.Helper()
	calGit(t, root, "add", "-A")
	calGit(t, root, "commit", "-q", "-m", message)
	return calGit(t, root, "rev-parse", "HEAD")
}

func calProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	calGit(t, root, "init", "-q")
	return root
}

func calExists(name string) bool {
	_, err := os.Lstat(name)
	return err == nil
}

// calJSON writes v to target as JSON, making its folder.
func calJSON(t *testing.T, target string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	calWrite(t, filepath.Dir(target), filepath.Base(target), string(data)+"\n")
}

// realTrees are the tree guards of the registered agents.
func realTrees() calibration.Trees {
	trees := calibration.Trees{}
	for _, a := range wiring.Agents() {
		trees[a.Facts.Name] = a.Tree
	}
	return trees
}

// claudeGateJSON is the Claude settings duolog install writes: the gate on
// every tool.
func claudeGateJSON(t *testing.T) string {
	t.Helper()
	hooks, err := launchtest.HooksJSON("claude", 150)
	if err != nil {
		t.Fatal(err)
	}
	return string(hooks)
}

// cellFixture is a project whose last commit adds parse.go and its hidden
// test parse_test.go, with the task files of that commit in a run folder.
type cellFixture struct {
	root, runDir string
	// home is the temporary home; slot is the cell's tree, which its Codex
	// config trusts.
	home, slot string
	task       calibration.TaskRecord
	cell       calibration.Cell
	env        calibration.CellEnv
	checkCalls int
	// deps are the dependencies of the real delegation: the real functions,
	// but an admission gate whose quota reads start no binary and whose
	// account state is temporary, never the user's. sharedGate replaces it.
	deps delegate.Deps
}

func newCellFixture(t *testing.T) *cellFixture {
	t.Helper()
	return newCellFixtureWith(t, nil)
}

// newCellFixtureWith is newCellFixture with files more in the parent.
func newCellFixtureWith(t *testing.T, parentFiles map[string]string) *cellFixture {
	t.Helper()
	cfg := calConfig(t)
	home := calHome(t)
	root := calProject(t)
	calWrite(t, root, "go.mod", "module widget\n")
	calWrite(t, root, "widget.go", "package widget\n")
	for name, body := range parentFiles {
		calWrite(t, root, name, body)
	}
	parent := calCommit(t, root, "start")
	calWrite(t, root, "parse.go", "package widget\n\nimport \"strings\"\n\nfunc Parse(line string) []string {\n\treturn strings.Split(line, \",\")\n}\n")
	calWrite(t, root, "parse_test.go", "package widget\n\nimport \"testing\"\n\nfunc TestParse(t *testing.T) {\n\tif len(Parse(\"a,b\")) != 2 {\n\t\tt.Fatal(\"parse\")\n\t}\n}\n")
	ref := calCommit(t, root, "add widget parser")
	calWrite(t, root, ".codex/hooks.json", gateHooks)
	calCommit(t, root, "add the gate")
	slot, err := calibration.TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	trustCodex(t, home, slot, true, true)

	stateDir := filepath.Join(root, ".duolog")
	runDir := filepath.Join(calibration.RunsDir(stateDir), "cal-1")
	c := calibration.Candidate{
		ID: "k-parse", Parent: parent, Ref: ref, Commits: []string{ref},
		StatementSource: "plan", Statement: parseStatement, Subject: "add widget parser",
		Hidden: []string{"parse_test.go"}, HiddenNames: []string{"TestParse"},
		Class: "implement", Features: map[string]string{"output": "code", "scope": "1-3", "components": "1"},
	}
	if err := calibration.SaveManifest(runDir, calibration.Manifest{Verification: map[string]string{c.ID: "hidden"}}); err != nil {
		t.Fatal(err)
	}
	if err := calibration.WriteTaskFiles(context.Background(), runDir, root, c); err != nil {
		t.Fatal(err)
	}
	task, err := calibration.ReadTask(runDir, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	class := cfg.Learning.Classes["implement"]
	class.Checks = []string{"go test ./..."}
	cfg.Learning.Classes["implement"] = class
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &cellFixture{root: root, runDir: runDir, home: home, slot: slot, task: task,
		cell: calibration.Cell{ID: "c-1", Task: c.ID, Class: c.Class, Entry: "codex/sol/high", Round: 1, State: calibration.CellRunning}}
	f.deps = wiring.DelegateDeps()
	f.deps.PaidGate = paidrunstest.Factory(t.TempDir())
	f.env = calibration.CellEnv{Root: root, StateDir: stateDir, RunDir: runDir, Lead: "lead-1", Cfg: cfg, Store: s,
		Deps: calibration.Deps{
			Delegate: func(context.Context, delegate.Request) (delegate.Outcome, error) {
				t.Fatal("delegate called")
				return delegate.Outcome{}, nil
			},
			Verify:  calibration.VerifyIsolated,
			Environ: launchtest.Environ,
			Trees:   realTrees(),
		},
		Checks: func(_ context.Context, dir string, commands []string) []journal.Check {
			f.checkCalls++
			exit := 0
			if !calExists(filepath.Join(dir, "parse_test.go")) {
				t.Errorf("checks ran without the hidden test in %s", dir)
				exit = 1
			}
			var checks []journal.Check
			for _, command := range commands {
				checks = append(checks, journal.Check{Command: command, Exit: exit, Seconds: 1})
			}
			return checks
		},
	}
	return f
}

// realClaude gives the cell a Claude entry and runs it through the real
// delegation, task inlining and worker runner, with a fake claude binary
// that answers a worker run with worker. It returns the binary's path; its
// recordings hold the last run's argv. outcome receives what the delegation
// returned.
func (f *cellFixture) realClaude(t *testing.T, worker testbin.Scenario, outcome *delegate.Outcome) string {
	t.Helper()
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	// duolog calibrate runs in the project, which delegation reads from the
	// working folder.
	t.Chdir(f.root)
	reg, err := session.Open(f.env.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-1", Target: config.Target{Agent: "codex", Model: "sol", Effort: "high"}, Mode: mode.Edits}); err != nil {
		t.Fatal(err)
	}
	f.cell.Entry = "claude/opus/high"
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		out, err := delegate.Run(ctx, f.deps, r)
		*outcome = out
		return out, err
	}
	// Both calls start with the worker process's --settings, whose JSON names
	// the cell's tree, so they are told apart by order: the login check runs
	// on that process first, then the worker.
	return testbin.Install(t, "claude", testbin.Scenario{Sequence: []testbin.Scenario{
		{Stdout: `{"loggedIn":true}`},
		worker,
	}})
}

// argv is the argument list the fake binary at path last ran with.
func argv(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path + ".args.json")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	return args
}

func (f *cellFixture) run(t *testing.T) calibration.CellResult {
	t.Helper()
	r, err := calibration.RunCell(context.Background(), f.env, f.cell, f.task, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *cellFixture) cellFile(name string) string {
	return filepath.Join(f.runDir, calibration.CellsDir, f.cell.ID, name)
}

// worked is a delegate that changes parse.go in its work tree and finishes.
func worked(t *testing.T) delegate.Runner {
	return func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		calWrite(t, r.Options.WorkRoot, "parse.go", "package widget\n\nfunc Parse(line string) []string { return nil }\n")
		return delegate.Outcome{Result: corerun.Result{Done: true}}, nil
	}
}

// onlyHooks fails unless slot holds the project's Codex hooks and nothing
// else.
func onlyHooks(t *testing.T, slot string) {
	t.Helper()
	var names []string
	err := filepath.WalkDir(slot, func(name string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(slot, name)
			names = append(names, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil || !slices.Equal(names, []string{".codex/hooks.json"}) {
		t.Fatalf("slot %s holds %v, %v; want only .codex/hooks.json", slot, names, err)
	}
	if body, err := os.ReadFile(filepath.Join(slot, ".codex", "hooks.json")); err != nil || string(body) != gateHooks {
		t.Fatalf("hooks.json in the slot = %q, %v; want the project's", body, err)
	}
}

// fakeQuota answers every agent with a weekly and a five-hour window.
type fakeQuota struct {
	weekly, window float64
	reset          time.Time
}

func (q *fakeQuota) read(context.Context, string) ([]calibration.Window, error) {
	return []calibration.Window{
		{Kind: calibration.KindWeekly, UsedPercent: q.weekly, ResetsAt: q.reset.Add(72 * time.Hour)},
		{Kind: calibration.KindFiveHour, UsedPercent: q.window, ResetsAt: q.reset},
	}, nil
}

// sharedGate makes every delegation of the fixture admit through one gate,
// as one user's runs do.
func sharedGate(t *testing.T, f *cellFixture) *paidruns.Gate {
	t.Helper()
	g, err := paidrunstest.Gate(filepath.Join(t.TempDir(), "accounts"), f.env.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.deps.PaidGate = func(*config.Config) (*paidruns.Gate, error) { return g, nil }
	return g
}

func gateState(t *testing.T, g *paidruns.Gate) paidruns.State {
	t.Helper()
	var st paidruns.State
	if err := g.Store.View(context.Background(), func(s paidruns.State) error { st = s; return nil }); err != nil {
		t.Fatal(err)
	}
	return st
}

// callCount counts the calls of a fake binary.
func callCount(t *testing.T, bin string) int {
	t.Helper()
	data, err := os.ReadFile(bin + ".calls.ndjson")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// A Claude cell gets the turn cap as --max-turns. A worker that reaches it
// ends the cell as failed_limit with reason "turn cap" and keeps its usage,
// without checks: Codex has no such cap, so it is no result, not a failure.
// Claude reports the cap with no answer.
func TestRunCellTurnCapThroughClaude(t *testing.T) {
	f := newCellFixture(t)
	var out delegate.Outcome
	fake := f.realClaude(t, testbin.Scenario{Exit: 1, Stdout: `{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":101,"session_id":"worker-1","usage":{"input_tokens":30,"output_tokens":5}}` + "\n"}, &out)
	r := f.run(t)
	if r.State != calibration.CellFailedLimit || r.Passed || r.Reason != "turn cap" {
		t.Fatalf("result %+v, want failed_limit, not passed, turn cap (delegation line %q)", r, out.Line)
	}
	if r.Tokens != 35 {
		t.Fatalf("Tokens = %d, want 35", r.Tokens)
	}
	if f.checkCalls != 0 {
		t.Fatalf("checks ran %d times after the turn cap", f.checkCalls)
	}
	args := argv(t, fake)
	if i := slices.Index(args, "--max-turns"); i < 0 || i+1 == len(args) || args[i+1] != "100" {
		t.Fatalf("worker argv %q, want --max-turns 100", args)
	}
}

// A Claude cell's tree has the duolog gate active even when the project's
// gate is installed at project scope only and the replayed parent predates
// it: the worker runs with rules, not "sandbox only".
func TestRunCellClaudeTreeHasGate(t *testing.T) {
	f := newCellFixture(t)
	// Installed after the parent and not committed.
	calWrite(t, f.root, ".claude/settings.json", claudeGateJSON(t))
	var out delegate.Outcome
	fake := f.realClaude(t, testbin.Scenario{Stdout: parseAnswer, Writes: map[string]string{"parse.go": "package widget\n\nfunc Parse(line string) []string { return nil }\n"}}, &out)
	r := f.run(t)
	if r.State != calibration.CellDone || !r.Passed {
		t.Fatalf("result %+v, want done and passed (delegation line %q)", r, out.Line)
	}
	if strings.Contains(out.Line, "no duolog hooks") || strings.Contains(out.Line, "rules are off") {
		t.Fatalf("the worker ran without the gate: %q", out.Line)
	}
	if !strings.Contains(string(r.Patch), "parse.go") || strings.Contains(string(r.Patch), ".claude") {
		t.Fatalf("patch holds the candidate's change only:\n%s", r.Patch)
	}
	if cwd, err := os.ReadFile(fake + ".cwd"); err != nil || pathutil.Resolved(string(cwd)) != pathutil.Resolved(f.slot) {
		t.Fatalf("worker ran in %q, %v; want the slot %s", cwd, err, f.slot)
	}
}

// A cell's worker gets the telemetry and balance roots as every delegated
// worker does: the delegation carries them and the Claude worker's launch
// settings deny them.
func TestRunCellWorkerGetsProtectedRoots(t *testing.T) {
	f := newCellFixture(t)
	roots := []string{filepath.Join(f.home, "state", "duolog", "telemetry"), filepath.Join(f.home, "state", "duolog", "balance")}
	f.env.ProtectedRoots = roots
	var out delegate.Outcome
	fake := f.realClaude(t, testbin.Scenario{Stdout: parseAnswer, Writes: map[string]string{"parse.go": "package widget\n\nfunc Parse(line string) []string { return nil }\n"}}, &out)
	delegateRun := f.env.Delegate
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		if got := r.Options.ProtectedRoots; !slices.Equal(got, roots) {
			t.Errorf("delegation roots %q, want %q", got, roots)
		}
		return delegateRun(ctx, r)
	}
	if r := f.run(t); r.State != calibration.CellDone {
		t.Fatalf("result %+v, want done (delegation line %q)", r, out.Line)
	}
	args := argv(t, fake)
	i := slices.Index(args, "--settings")
	if i < 0 || i+1 == len(args) {
		t.Fatalf("worker argv %q has no settings", args)
	}
	var settings struct {
		Sandbox struct {
			Filesystem struct {
				DenyRead []string `json:"denyRead"`
			} `json:"filesystem"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(args[i+1]), &settings); err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		if !slices.Contains(settings.Sandbox.Filesystem.DenyRead, filepath.ToSlash(root)) {
			t.Fatalf("worker settings %s do not deny %s", args[i+1], root)
		}
	}
}

// disableAllHooks turns the gate off in every layer Claude loads: from the
// replayed commit's project settings or from the person's user settings, it
// refuses the cell before the worker starts. The export leaves a committed
// local settings file out, so that layer holds only duolog's gate.
func TestRunCellRefusesClaudeTreeWithHooksOff(t *testing.T) {
	off := `{"disableAllHooks":true}`
	gate := claudeGateJSON(t)
	for _, tc := range []struct {
		name         string
		parent       map[string]string
		userSettings string
	}{
		{"project settings", map[string]string{".claude/settings.json": off}, ""},
		{"project settings with the gate", map[string]string{".claude/settings.json": `{"disableAllHooks":true,` + gate[1:]}, ""},
		{"user settings", nil, off},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCellFixtureWith(t, tc.parent)
			if tc.userSettings != "" {
				calWrite(t, filepath.Join(f.home, ".claude"), "settings.json", tc.userSettings)
			}
			var out delegate.Outcome
			fake := f.realClaude(t, testbin.Scenario{Stdout: parseAnswer}, &out)
			started := false
			_, err := calibration.RunCell(context.Background(), f.env, f.cell, f.task, func() (bool, error) { started = true; return true, nil })
			if err == nil || !strings.Contains(err.Error(), "disableAllHooks") || !strings.Contains(err.Error(), f.slot) {
				t.Fatalf("RunCell with hooks off = %v, want a refusal naming disableAllHooks and %s", err, f.slot)
			}
			if started {
				t.Fatal("a cell refused for hooks off was admitted; the refusal must come before start")
			}
			if calExists(fake + ".args.json") {
				t.Fatalf("the worker ran with hooks off: %q", argv(t, fake))
			}
			if calExists(f.cellFile("result.json")) {
				t.Fatal("a refused cell saved a result")
			}
		})
	}
}

// The first run with project-only Codex hooks: the refusal leaves the hooks
// the person must approve in the slot and nothing else, and once they are
// approved the next run works there.
func TestRunCellRefusesUntrustedTree(t *testing.T) {
	f := newCellFixture(t)
	trustCodex(t, f.home, f.slot, false, false)
	_, err := calibration.RunCell(context.Background(), f.env, f.cell, f.task, nil)
	if err == nil || !strings.Contains(err.Error(), f.slot) || !strings.Contains(err.Error(), "hooks") {
		t.Fatalf("RunCell on an untrusted tree: err = %v, want one naming %s and its hooks", err, f.slot)
	}
	onlyHooks(t, f.slot)
	if calExists(f.cellFile("result.json")) {
		t.Fatal("a refused cell saved a result")
	}

	trustCodex(t, f.home, f.slot, true, true)
	f.env.Delegate = worked(t)
	if r := f.run(t); r.State != calibration.CellDone || !r.Passed {
		t.Fatalf("result %+v after approving the hooks, want done and passed", r)
	}
	onlyHooks(t, f.slot)
}

// A real cell takes one account permit after its start: it runs its
// delegation in delegate's worker process, after its isolation and trust
// setup and its StartFunc, and settles once in the shared ledger; a step
// refused before it starts holds and spends nothing. The runner hands the
// delegation the budget its start set through the step's context, which is
// calibrate's own; TestCalibrationOnePermitPerActualAttempt there checks that
// RunCell passes it on, and this test puts it on the request.
func TestCalibrationRealCellTakesOnePermit(t *testing.T) {
	f := newCellFixture(t)
	g := sharedGate(t, f)
	var out delegate.Outcome
	fake := f.realClaude(t, testbin.Scenario{Stdout: parseAnswer, Writes: map[string]string{"parse.go": "package widget\n\nfunc Parse(line string) []string { return nil }\n"}}, &out)
	var budget *paidrules.StepBudget
	delegateRun := f.env.Delegate
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		r.Options.Budget = budget
		return delegateRun(ctx, r)
	}
	ctx := context.Background()
	start := func() (bool, error) {
		budget = &paidrules.StepBudget{PlanHash: "plan-h", StepID: "c-1.1", USDRemaining: 10, WeeklyRemaining: 0.1, WindowRemaining: 0.5, WaitForSession: true}
		return true, nil
	}
	r, err := calibration.RunCell(ctx, f.env, f.cell, f.task, start)
	if err != nil || r.State != calibration.CellDone {
		t.Fatalf("cell: %+v %v (%q)", r, err, out.Line)
	}
	st := gateState(t, g)
	if len(st.Reservations) != 0 || len(st.Settled) != 1 {
		t.Fatalf("one permit settled once: %+v %+v", st.Reservations, st.Settled)
	}
	for _, s := range st.Settled {
		if !s.Started || s.Input == 0 {
			t.Fatalf("settlement: %+v", s)
		}
	}
	if r.Input == 0 || r.Output == 0 {
		t.Fatalf("the cell lost its token split: %+v", r)
	}
	calls := callCount(t, fake)

	refused := func() (bool, error) { return false, nil }
	if _, err := calibration.RunCell(ctx, f.env, f.cell, f.task, refused); !errors.Is(err, calibration.ErrNotAdmitted) {
		t.Fatalf("refused start: %v", err)
	}
	if st := gateState(t, g); len(st.Settled) != 1 || len(st.Reservations) != 0 || callCount(t, fake) != calls {
		t.Fatal("a refused step reserved, spent or ran")
	}
}

// TestCellUSDTakesReportedCost is review finding I4, second half: a Claude
// cell whose CLI reports a cost above its token price is charged that cost.
func TestCellUSDTakesReportedCost(t *testing.T) {
	f := newCellFixture(t)
	var out delegate.Outcome
	answer := `{"session_id":"worker-1","usage":{"input_tokens":1000,"output_tokens":200},"total_cost_usd":5,` +
		`"structured_output":{"done":true,"summary":"Added Parse.","files":["parse.go"],"needs":[]}}` + "\n"
	f.realClaude(t, testbin.Scenario{Stdout: answer, Writes: map[string]string{
		"parse.go": "package widget\n\nfunc Parse(line string) []string { return nil }\n"}}, &out)
	r := f.run(t)
	if r.USD != 5 {
		t.Fatalf("cell USD %v (%q), want the reported 5", r.USD, out.Line)
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
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		workers++
		return work(ctx, r)
	}
	run := &calibration.Run{ID: "cal-1", Step: "plan", Created: calTime, Approved: &calibration.Approval{Hash: "h", Time: calTime},
		Cells: []calibration.Cell{{ID: "c001", Task: f.task.ID, Class: "implement", Entry: "codex/sol/high", Round: 1, State: calibration.CellPending}}}
	if err := run.Save(f.env.StateDir); err != nil {
		t.Fatal(err)
	}
	m, err := calibration.LoadManifest(f.runDir)
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQuota{weekly: 20, window: 20, reset: calTime.Add(time.Hour)}
	var out bytes.Buffer
	runner := func(run *calibration.Run) *calibration.Runner {
		return &calibration.Runner{StateDir: f.env.StateDir, Root: f.root, Cfg: f.env.Cfg, Run: run, Plan: calibration.Plan{Run: run.ID, CapWeekly: 10, CapWindow: 50}, Manifest: m,
			Quota: &calibration.QuotaCache{Read: q.read, Now: func() time.Time { return calTime }}, Now: func() time.Time { return calTime },
			Cell: func(ctx context.Context, c calibration.Cell, t calibration.TaskRecord, start calibration.StartFunc) (calibration.CellResult, error) {
				return calibration.RunCell(ctx, f.env, c, t, start)
			},
			Rewrite: func(context.Context, calibration.TaskRecord, calibration.StartFunc) (string, int64, error) {
				return "", 0, errors.New("no rewrite")
			},
			Out: &out}
	}
	for range 2 {
		if _, err := runner(run).Execute(context.Background()); err == nil || !strings.Contains(err.Error(), f.slot) {
			t.Fatalf("Execute on an untrusted tree = %v, want a refusal naming %s", err, f.slot)
		}
		saved, err := calibration.LoadRun(f.env.StateDir, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := saved.Spend.WeeklyPct["codex"]; got != 0 || workers != 0 {
			t.Fatalf("after a trust refusal: spend %v, %d workers; want nothing charged and no worker", got, workers)
		}
		if saved.Cells[0].State != calibration.CellPending {
			t.Fatalf("cell %+v after a trust refusal, want pending", saved.Cells[0])
		}
		run = saved
	}

	trustCodex(t, f.home, f.slot, true, true)
	o, err := runner(run).Resume(context.Background())
	if err != nil || !o.Done {
		t.Fatalf("Resume = %+v, %v; want done:\n%s", o, err, out.String())
	}
	saved, err := calibration.LoadRun(f.env.StateDir, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Spend.WeeklyPct["codex"]; got != calibration.BoundWeekly || workers != 1 || saved.Cells[0].State != calibration.CellDone {
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
	run := &calibration.Run{ID: "cal-1", Step: "plan", Created: calTime, Approved: &calibration.Approval{Hash: "h", Time: calTime},
		Cells: []calibration.Cell{{ID: "c001", Task: f.task.ID, Class: "implement", Entry: f.cell.Entry, Round: 1, State: calibration.CellPending}}}
	if err := run.Save(f.env.StateDir); err != nil {
		t.Fatal(err)
	}
	m, err := calibration.LoadManifest(f.runDir)
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQuota{weekly: 20, window: 20, reset: calTime.Add(time.Hour)}
	r := &calibration.Runner{StateDir: f.env.StateDir, Root: f.root, Cfg: f.env.Cfg, Run: run, Plan: calibration.Plan{Run: run.ID, CapWeekly: 10, CapWindow: 50}, Manifest: m,
		Quota: &calibration.QuotaCache{Read: q.read, Now: func() time.Time { return calTime }}, Now: func() time.Time { return calTime },
		Cell: func(ctx context.Context, c calibration.Cell, t calibration.TaskRecord, start calibration.StartFunc) (calibration.CellResult, error) {
			return calibration.RunCell(ctx, f.env, c, t, start)
		},
		Rewrite: func(context.Context, calibration.TaskRecord, calibration.StartFunc) (string, int64, error) {
			return "", 0, errors.New("no rewrite")
		},
		Out: &bytes.Buffer{}}
	if _, err := r.Execute(context.Background()); err == nil || !strings.Contains(err.Error(), "lead-gone") {
		t.Fatalf("Execute = %v, want the unknown lead refused", err)
	}
	if _, err := os.Stat(fake + ".args.json"); !os.IsNotExist(err) {
		t.Fatalf("a worker ran for a refused delegation: %v", err)
	}
	saved, err := calibration.LoadRun(f.env.StateDir, run.ID)
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
	if c := saved.Cells[0]; c.State != calibration.CellPending || c.Tree != "" {
		t.Fatalf("cell %+v after a refusal before the worker, want pending without a tree", c)
	}
}

// A Codex judge runs only in a judge slot Codex trusts with its hooks. The
// run has one task whose two cells, a and b, passed; the delegate counts the
// judge calls, of which there must be none.
func TestJudgeRefusesUntrustedSlot(t *testing.T) {
	a, b := "claude/opus/high", "claude/sonnet/medium"
	cfg := calConfig(t)
	home := calHome(t)
	// The project's gate goes into the judge slot, which Codex trusts.
	root := t.TempDir()
	calWrite(t, filepath.Join(root, ".codex"), "hooks.json", gateHooks)
	slot, err := calibration.JudgeSlot(root)
	if err != nil {
		t.Fatal(err)
	}
	trustCodex(t, home, slot, true, true)
	stateDir := t.TempDir()
	run := &calibration.Run{ID: "cal-1", Step: "run", Created: calTime, Cells: []calibration.Cell{
		{ID: "c-1", Task: "k-1", Class: "implement", Entry: b, Round: 1, State: calibration.CellDone, Passed: true},
		{ID: "c-2", Task: "k-1", Class: "implement", Entry: a, Round: 1, State: calibration.CellDone, Passed: true},
	}}
	if err := run.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	runDir := run.Dir(stateDir)
	task := calibration.TaskRecord{ManifestTask: calibration.ManifestTask{ID: "k-1", Statement: parseStatement, Class: "implement", Verification: "hidden",
		Features: map[string]string{"output": "code"}}}
	calJSON(t, filepath.Join(runDir, calibration.TasksDir, task.ID, "task.json"), task)
	for name, body := range map[string]string{"statement.txt": parseStatement + "\n", "reference.patch": "+reference\n"} {
		calWrite(t, filepath.Join(runDir, calibration.TasksDir, task.ID), name, body)
	}
	for id, patch := range map[string]string{"c-1": "+// beta side\n", "c-2": "+// alpha side\n"} {
		dir := filepath.Join(runDir, calibration.CellsDir, id)
		calWrite(t, dir, "result.patch", patch)
		calJSON(t, filepath.Join(dir, "checks.json"), struct {
			Hidden []journal.Check `json:"hidden,omitempty"`
		}{Hidden: []journal.Check{{Command: "go test ./...", Seconds: 1}}})
	}
	calls := 0
	env := calibration.JudgeEnv{
		CellEnv: calibration.CellEnv{Root: root, StateDir: stateDir, RunDir: runDir, Lead: "lead-1", Cfg: cfg,
			Deps: calibration.Deps{Environ: launchtest.Environ, Trees: realTrees(), Delegate: func(context.Context, delegate.Request) (delegate.Outcome, error) {
				calls++
				return delegate.Outcome{Result: corerun.Result{Done: true}}, nil
			}}},
		Journal: &journal.Journal{Dir: t.TempDir()},
	}
	env.Rand = func() bool { return true }

	trustCodex(t, home, slot, true, false)
	_, err = calibration.JudgePair(context.Background(), env, run, calibration.Pair{Task: "k-1", Class: "implement", A: a, B: b}, task)
	if err == nil || !strings.Contains(err.Error(), "Codex must trust") || !strings.Contains(err.Error(), slot) {
		t.Fatalf("err = %v, want a trust refusal naming %s", err, slot)
	}
	if calls != 0 {
		t.Fatalf("%d judge calls in an untrusted slot", calls)
	}
	// The hooks stay in the slot for the person to approve.
	if _, err := os.Stat(filepath.Join(slot, ".codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
}

package calibration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const parseStatement = "Add a function Parse(line string) []string that splits line on commas."

// cellFixture is a project whose last commit adds parse.go and its hidden
// test parse_test.go, with the task files of that commit in a run folder.
type cellFixture struct {
	root, runDir string
	// home is the temporary home; slot is the cell's tree, which its Codex
	// config trusts.
	home, slot string
	task       TaskRecord
	cell       Cell
	env        CellEnv
	checkCalls int
}

func newCellFixture(t *testing.T) *cellFixture {
	t.Helper()
	return newCellFixtureWith(t, nil)
}

// newCellFixtureWith is newCellFixture with files more in the parent.
func newCellFixtureWith(t *testing.T, parentFiles map[string]string) *cellFixture {
	t.Helper()
	cfg := testConfig(t)
	home := tempHome(t)
	root := project(t)
	write(t, root, "go.mod", "module widget\n")
	write(t, root, "widget.go", "package widget\n")
	for name, body := range parentFiles {
		write(t, root, name, body)
	}
	parent := commit(t, root, "start")
	write(t, root, "parse.go", "package widget\n\nimport \"strings\"\n\nfunc Parse(line string) []string {\n\treturn strings.Split(line, \",\")\n}\n")
	write(t, root, "parse_test.go", "package widget\n\nimport \"testing\"\n\nfunc TestParse(t *testing.T) {\n\tif len(Parse(\"a,b\")) != 2 {\n\t\tt.Fatal(\"parse\")\n\t}\n}\n")
	ref := commit(t, root, "add widget parser")
	write(t, root, ".codex/hooks.json", gateHooks)
	commit(t, root, "add the gate")
	slot, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	trustCodex(t, home, slot, true, true)

	stateDir := filepath.Join(root, ".duolog")
	runDir := filepath.Join(RunsDir(stateDir), "cal-1")
	c := Candidate{
		ID: "k-parse", Parent: parent, Ref: ref, Commits: []string{ref},
		StatementSource: "plan", Statement: parseStatement, Subject: "add widget parser",
		Hidden: []string{"parse_test.go"}, HiddenNames: []string{"TestParse"},
		Class: "implement", Features: map[string]string{"output": "code", "scope": "1-3", "components": "1"},
	}
	if err := SaveManifest(runDir, Manifest{Verification: map[string]string{c.ID: "hidden"}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteTaskFiles(context.Background(), runDir, root, c); err != nil {
		t.Fatal(err)
	}
	task, err := ReadTask(runDir, c.ID)
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
		cell: Cell{ID: "c-1", Task: c.ID, Class: c.Class, Entry: "codex/sol/high", Round: 1, State: CellRunning}}
	f.env = CellEnv{Root: root, StateDir: stateDir, RunDir: runDir, Lead: "lead-1", Cfg: cfg, Store: s,
		Delegate: func(context.Context, delegate.Request) (delegate.Outcome, error) {
			t.Fatal("delegate called")
			return delegate.Outcome{}, nil
		},
		Checks: func(_ context.Context, dir string, commands []string) []journal.Check {
			f.checkCalls++
			exit := 0
			if !exists(filepath.Join(dir, "parse_test.go")) {
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

// parseAnswer is a Claude worker's output after it wrote parse.go.
const parseAnswer = `{"session_id":"worker-1","usage":{"input_tokens":1000,"output_tokens":200},"structured_output":{"done":true,"summary":"Added Parse.","files":["parse.go"],"needs":[]}}` + "\n"

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
		out, err := delegate.Run(ctx, realDeps, r)
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

func (f *cellFixture) run(t *testing.T) CellResult {
	t.Helper()
	r, err := RunCell(context.Background(), f.env, f.cell, f.task, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *cellFixture) cellFile(name string) string {
	return filepath.Join(f.runDir, "cells", f.cell.ID, name)
}

// emptied fails unless tree is the cell's slot and the cell left only the
// project's Codex hooks there, which the person approved in that place.
func (f *cellFixture) emptied(t *testing.T, tree string) {
	t.Helper()
	if tree != f.slot {
		t.Fatalf("work tree %q, want the slot %s", tree, f.slot)
	}
	onlyHooks(t, tree)
}

func TestRunCellIsolatedAndChecked(t *testing.T) {
	f := newCellFixture(t)
	var tree string
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		role, taskPath, opts := r.Role, r.TaskPath, r.Options
		tree = opts.WorkRoot
		if !opts.Calibration || opts.Lead != "lead-1" || opts.Entry == nil || opts.Entry.Agent != "codex" || opts.Entry.Model != "sol" || opts.Entry.Effort != "high" {
			t.Errorf("options %+v", opts)
		}
		if role != "implement" {
			t.Errorf("role %q, want implement", role)
		}
		if taskPath != f.cellFile("task.md") {
			t.Errorf("task %s, want %s", taskPath, f.cellFile("task.md"))
		}
		if inside(f.root, tree) {
			t.Errorf("work root %s is inside the project %s", tree, f.root)
		}
		if err := exec.Command("git", "-C", tree, "cat-file", "-e", f.task.Ref).Run(); err == nil {
			t.Errorf("reference commit %s is reachable in the work tree", f.task.Ref)
		}
		write(t, tree, "parse.go", "package widget\n\nfunc Parse(line string) []string { return nil }\n")
		return delegate.Outcome{Result: runner.Result{Done: true, Usage: runner.Usage{Input: 1000, Output: 200}}}, nil
	}
	r := f.run(t)
	if r.State != CellDone || !r.Passed || r.Reason != "" {
		t.Fatalf("result %+v, want done and passed", r)
	}
	if r.Tokens != 1200 {
		t.Fatalf("Tokens = %d, want 1200", r.Tokens)
	}
	if !strings.Contains(string(r.Patch), "parse.go") || strings.Contains(string(r.Patch), "parse_test.go") {
		t.Fatalf("patch holds the candidate's change only:\n%s", r.Patch)
	}
	if f.checkCalls != 1 || len(r.Hidden) != 1 || r.Hidden[0].Command != "go test ./..." {
		t.Fatalf("checks: %d calls, hidden %+v", f.checkCalls, r.Hidden)
	}
	f.emptied(t, tree)
	for _, name := range []string{"result.patch", "result.json", "checks.json"} {
		if !exists(f.cellFile(name)) {
			t.Fatalf("%s is missing", name)
		}
	}
}

func TestRunCellTimeCap(t *testing.T) {
	f := newCellFixture(t)
	old := cellTimeout
	t.Cleanup(func() { cellTimeout = old })
	cellTimeout = func(config.Calibration) time.Duration { return 50 * time.Millisecond }
	var tree string
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		tree = r.Options.WorkRoot
		<-ctx.Done()
		return delegate.Outcome{}, ctx.Err()
	}
	r := f.run(t)
	if r.State != CellDone || r.Passed || r.Reason != "time cap" {
		t.Fatalf("result %+v, want done, not passed, time cap", r)
	}
	if f.checkCalls != 0 {
		t.Fatalf("checks ran %d times after the time cap", f.checkCalls)
	}
	f.emptied(t, tree)
}

// makeFixture is a cell fixture whose class check is `make test`, run by the
// real check runner: the Makefile runs tests/check_test.sh, which passes
// once calc.txt says fixed, as the reference makes it. With refMakefile the
// reference changes the Makefile too.
func makeFixture(t *testing.T, refMakefile bool) *cellFixture {
	t.Helper()
	f := newCellFixture(t)
	root := project(t)
	write(t, root, "Makefile", "test:\n\tsh tests/check_test.sh\n")
	write(t, root, "check.sh", "make test\n")
	write(t, root, "tests/check_test.sh", "grep -q fixed calc.txt\n")
	write(t, root, "calc.txt", "broken\n")
	write(t, root, ".codex/hooks.json", gateHooks)
	parent := commit(t, root, "start")
	write(t, root, "calc.txt", "fixed\n")
	if refMakefile {
		write(t, root, "Makefile", "# checks\ntest:\n\tsh tests/check_test.sh\n")
	}
	ref := commit(t, root, "fix calc")
	runDir := filepath.Join(RunsDir(filepath.Join(root, ".duolog")), "cal-1")
	c := Candidate{ID: "k-calc", Parent: parent, Ref: ref, Commits: []string{ref}, StatementSource: "plan",
		Statement: "Make calc.txt say fixed.", Subject: "fix calc", Class: "implement", Features: map[string]string{"output": "code"}}
	if err := SaveManifest(runDir, Manifest{Verification: map[string]string{c.ID: "check"}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteTaskFiles(context.Background(), runDir, root, c); err != nil {
		t.Fatal(err)
	}
	task, err := ReadTask(runDir, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	slot, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	trustCodex(t, f.home, slot, true, true)
	f.root, f.runDir, f.slot, f.task = root, runDir, slot, task
	f.cell.Task = c.ID
	f.env.Root, f.env.StateDir, f.env.RunDir, f.env.Store = root, filepath.Join(root, ".duolog"), runDir, s
	f.env.Checks = profile.RunChecks
	class := f.env.Cfg.Learning.Classes["implement"]
	class.Checks = []string{"make test"}
	f.env.Cfg.Learning.Classes["implement"] = class
	// The candidate makes the check pass without the fix: it turns the test
	// target into true and deletes the test.
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		tree := r.Options.WorkRoot
		write(t, tree, "Makefile", "test:\n\ttrue\n")
		if err := os.Remove(filepath.Join(tree, "tests", "check_test.sh")); err != nil {
			t.Error(err)
		}
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	}
	return f
}

// A candidate that changes the test target the check calls gets no result
// from its checks: the cell is check_setup_changed, for the person to decide
// in judge. The checks run on the candidate's Makefile, which is not put
// back, with the test it deleted put back. The patch keeps the change.
func TestRunCellMarksCheckSetupChanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses make and sh")
	}
	f := makeFixture(t, false)
	r := f.run(t)
	if r.State != CellSetupChanged || !r.Passed {
		t.Fatalf("result %+v, want check_setup_changed with its checks passing on true", r)
	}
	if want := []string{"Makefile"}; !slices.Equal(r.SetupChanged, want) {
		t.Fatalf("SetupChanged = %v, want %v", r.SetupChanged, want)
	}
	if want := []string{"tests/check_test.sh"}; !slices.Equal(r.Restored, want) {
		t.Fatalf("Restored = %v, want %v", r.Restored, want)
	}
	if !strings.Contains(string(r.Patch), "Makefile") {
		t.Fatalf("patch lacks the candidate's Makefile change:\n%s", r.Patch)
	}
	data, err := os.ReadFile(f.cellFile("result.json"))
	if err != nil || !strings.Contains(string(data), `"setup_changed"`) || !strings.Contains(string(data), `"restored"`) {
		t.Fatalf("result.json = %s, %v; want the changed setup and the restored test", data, err)
	}
}

// A Makefile the check reaches only through a file it runs is marked as
// well: the unchanged check.sh runs make test, and the candidate's Makefile
// turns that target into true. Without the mark the checks pass as an
// automatic pass.
func TestRunCellMarksIndirectMakefile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses make and sh")
	}
	f := makeFixture(t, false)
	class := f.env.Cfg.Learning.Classes["implement"]
	class.Checks = []string{"sh check.sh"}
	f.env.Cfg.Learning.Classes["implement"] = class
	r := f.run(t)
	if r.State != CellSetupChanged || !r.Passed || !slices.Equal(r.SetupChanged, []string{"Makefile"}) {
		t.Fatalf("result %+v, want check_setup_changed for Makefile with its checks passing on true", r)
	}
}

// A check setup file the reference changes too is marked all the same: the
// reference's change says nothing about the candidate's.
func TestRunCellMarksSetupTheReferenceChangesToo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses make and sh")
	}
	f := makeFixture(t, true)
	r := f.run(t)
	if r.State != CellSetupChanged || !slices.Equal(r.SetupChanged, []string{"Makefile"}) {
		t.Fatalf("result %+v, want check_setup_changed for Makefile", r)
	}
}

// npmFixture is a cell fixture whose parent has a package.json with a test
// script and a lock file, and whose class check is `npm test`. checked holds
// package.json and package-lock.json as the checks saw them.
func npmFixture(t *testing.T, checked map[string]string) *cellFixture {
	t.Helper()
	f := newCellFixtureWith(t, map[string]string{
		"package.json":      `{"scripts":{"test":"node test.js","build":"tsc"},"dependencies":{}}` + "\n",
		"package-lock.json": `{"lockfileVersion":3,"packages":{}}` + "\n",
	})
	class := f.env.Cfg.Learning.Classes["implement"]
	class.Checks = []string{"npm test"}
	f.env.Cfg.Learning.Classes["implement"] = class
	f.env.Checks = func(_ context.Context, dir string, commands []string) []journal.Check {
		for _, name := range []string{"package.json", "package-lock.json"} {
			data, _ := os.ReadFile(filepath.Join(dir, name))
			checked[name] = string(data)
		}
		var checks []journal.Check
		for _, command := range commands {
			checks = append(checks, journal.Check{Command: command, Seconds: 1})
		}
		return checks
	}
	return f
}

// A dependency the candidate declares stays in the tree the checks run on,
// with its lock file: the patch judged and the code checked are one
// solution.
func TestRunCellKeepsDependencyChanges(t *testing.T) {
	checked := map[string]string{}
	f := npmFixture(t, checked)
	manifest := `{"scripts":{"test":"node test.js","build":"tsc"},"dependencies":{"left-pad":"^1.3.0"}}` + "\n"
	lock := `{"lockfileVersion":3,"packages":{"node_modules/left-pad":{"version":"1.3.0"}}}` + "\n"
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		tree := r.Options.WorkRoot
		write(t, tree, "package.json", manifest)
		write(t, tree, "package-lock.json", lock)
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	}
	r := f.run(t)
	if r.State != CellDone || !r.Passed || len(r.Restored) != 0 || len(r.SetupChanged) != 0 {
		t.Fatalf("result %+v, want done and passed with nothing put back or marked", r)
	}
	if checked["package.json"] != manifest || checked["package-lock.json"] != lock {
		t.Fatalf("checks saw %q, want the candidate's manifest and lock file", checked)
	}
}

// The script a check runs is how the check is defined: changing it marks the
// cell, and the manifest is not put back.
func TestRunCellMarksCheckScriptChange(t *testing.T) {
	checked := map[string]string{}
	f := npmFixture(t, checked)
	manifest := `{"scripts":{"test":"true","build":"tsc"},"dependencies":{}}` + "\n"
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		write(t, r.Options.WorkRoot, "package.json", manifest)
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	}
	r := f.run(t)
	if r.State != CellSetupChanged || !slices.Equal(r.SetupChanged, []string{"package.json"}) || len(r.Restored) != 0 {
		t.Fatalf("result %+v, want check_setup_changed for package.json, nothing put back", r)
	}
	if checked["package.json"] != manifest {
		t.Fatalf("checks saw package.json %q, want the candidate's", checked["package.json"])
	}
}

// A script under scripts/ that an unchanged package script runs is check
// setup too: changing it marks the cell, though no check names it.
func TestRunCellMarksScriptAPackageScriptRuns(t *testing.T) {
	f := newCellFixtureWith(t, map[string]string{
		"package.json":     `{"scripts":{"test":"sh scripts/check.sh"}}` + "\n",
		"scripts/check.sh": "node test.js\n",
	})
	class := f.env.Cfg.Learning.Classes["implement"]
	class.Checks = []string{"npm test"}
	f.env.Cfg.Learning.Classes["implement"] = class
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		write(t, r.Options.WorkRoot, "scripts/check.sh", "true\n")
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	}
	r := f.run(t)
	if r.State != CellSetupChanged || !slices.Equal(r.SetupChanged, []string{"scripts/check.sh"}) {
		t.Fatalf("result %+v, want check_setup_changed for scripts/check.sh", r)
	}
}

// Files under ignored folders, such as an installed node_modules or a
// virtualenv, are no part of the check setup: the scan skips them and they
// stay for the checks.
func TestRunCellLeavesIgnoredFiles(t *testing.T) {
	f := newCellFixtureWith(t, map[string]string{".gitignore": "node_modules/\n.venv/\n"})
	ignored := []string{"node_modules/dep/package.json", "node_modules/dep/Makefile", ".venv/lib/conftest.py"}
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		tree := r.Options.WorkRoot
		for _, name := range ignored {
			write(t, tree, name, "installed\n")
		}
		write(t, tree, "parse.go", "package widget\n\nfunc Parse(line string) []string { return nil }\n")
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	}
	checks := f.env.Checks
	f.env.Checks = func(ctx context.Context, dir string, commands []string) []journal.Check {
		for _, name := range ignored {
			if !exists(filepath.Join(dir, filepath.FromSlash(name))) {
				t.Errorf("%s is gone before the checks", name)
			}
		}
		return checks(ctx, dir, commands)
	}
	r := f.run(t)
	if r.State != CellDone || !r.Passed || len(r.Restored) != 0 || len(r.SetupChanged) != 0 {
		t.Fatalf("result %+v, want done and passed with nothing put back or marked", r)
	}
}

// setupChanged names the files that define how a check runs, and only
// those: the scripts and targets the checks call, test runner and CI
// configuration and files a check names. Dependency declarations and lock
// files are the solution's.
func TestSetupChangedFindsCheckDefinitions(t *testing.T) {
	parent := map[string]string{
		"Makefile":         "test:\n\tsh scripts/check.sh\n",
		"scripts/check.sh": "go test ./...\n",
		"package.json":     `{"scripts":{"test":"jest","pretest":"lint","lint":"eslint ."},"jest":{"bail":true},"dependencies":{}}`,
		"pyproject.toml":   "[project]\nname = \"widget\"\ndependencies = []\n\n[tool.pytest.ini_options]\naddopts = \"-q\"\n",
		"setup.cfg":        "[metadata]\nname = widget\n\n[tool:pytest]\naddopts = -q\n",
		"go.mod":           "module widget\n",
	}
	for _, tc := range []struct {
		name    string
		checks  []string
		changes map[string]string
		want    []string
	}{
		{"makefile with make", []string{"make test"}, map[string]string{"Makefile": "# a comment\ntest:\n\tsh scripts/check.sh\n"}, []string{"Makefile"}},
		{"makefile without make", []string{"go test ./..."}, map[string]string{"Makefile": "test:\n\ttrue\n"}, []string{"Makefile"}},
		{"script under scripts", []string{"go test ./..."}, map[string]string{"scripts/check.sh": "true\n", "scripts/ci/new.sh": "true\n"}, []string{"scripts/check.sh", "scripts/ci/new.sh"}},
		{"other pyproject tool", []string{"pytest"}, map[string]string{"pyproject.toml": "[project]\nname = \"widget\"\ndependencies = []\n\n[tool.pytest.ini_options]\naddopts = \"-q\"\n\n[tool.coverage.run]\nomit = [\"*\"]\n"}, []string{"pyproject.toml"}},
		{"included makefile", []string{"make test"}, map[string]string{"mk/test.mk": "x:\n"}, []string{"mk/test.mk"}},
		{"file a check names", []string{"sh ./scripts/check.sh"}, map[string]string{"scripts/check.sh": "true\n"}, []string{"scripts/check.sh"}},
		{"script a check runs", []string{"npm test"}, map[string]string{"package.json": `{"scripts":{"test":"true","pretest":"lint","lint":"eslint ."},"jest":{"bail":true},"dependencies":{}}`}, []string{"package.json"}},
		{"pre script", []string{"npm run test"}, map[string]string{"package.json": `{"scripts":{"test":"jest","pretest":"","lint":"eslint ."},"jest":{"bail":true},"dependencies":{}}`}, []string{"package.json"}},
		{"script no check names", []string{"npm test"}, map[string]string{"package.json": `{"scripts":{"test":"jest","pretest":"lint","lint":"eslint .","e2e":"true"},"jest":{"bail":true},"dependencies":{}}`}, []string{"package.json"}},
		{"jest field", []string{"npm test"}, map[string]string{"package.json": `{"scripts":{"test":"jest","pretest":"lint","lint":"eslint ."},"jest":{"testMatch":[]},"dependencies":{}}`}, []string{"package.json"}},
		{"dependency", []string{"npm test"}, map[string]string{"package.json": `{"scripts":{"test":"jest","pretest":"lint","lint":"eslint ."},"jest":{"bail":true},"dependencies":{"left-pad":"1"}}`, "package-lock.json": "{}"}, nil},
		{"jest config", []string{"npm test"}, map[string]string{"jest.config.js": "module.exports = {}\n"}, []string{"jest.config.js"}},
		{"vitest config", []string{"npx vitest run"}, map[string]string{"web/vitest.config.ts": "export default {}\n"}, []string{"web/vitest.config.ts"}},
		{"ci config", []string{"go test ./..."}, map[string]string{".github/workflows/ci.yml": "on: push\n", ".gitlab-ci.yml": "test:\n"}, []string{".github/workflows/ci.yml", ".gitlab-ci.yml"}},
		{"pytest section", []string{"pytest"}, map[string]string{"pyproject.toml": "[project]\nname = \"widget\"\ndependencies = []\n\n[tool.pytest.ini_options]\naddopts = \"-q --co\"\n"}, []string{"pyproject.toml"}},
		{"python dependency", []string{"pytest"}, map[string]string{"pyproject.toml": "[project]\nname = \"widget\"\ndependencies = [\"attrs\"]\n\n[tool.pytest.ini_options]\naddopts = \"-q\"\n", "uv.lock": "x\n"}, nil},
		{"setup.cfg pytest section", []string{"pytest"}, map[string]string{"setup.cfg": "[metadata]\nname = widget\n\n[tool:pytest]\naddopts = -x\n"}, []string{"setup.cfg"}},
		{"setup.cfg metadata", []string{"pytest"}, map[string]string{"setup.cfg": "[metadata]\nname = gadget\n\n[tool:pytest]\naddopts = -q\n"}, nil},
		{"pytest files", []string{"pytest"}, map[string]string{"pytest.ini": "[pytest]\n", "tests/conftest.py": "x = 1\n", "tox.ini": "[tox]\n"}, []string{"pytest.ini", "tests/conftest.py", "tox.ini"}},
		{"deleted check file", []string{"sh scripts/check.sh"}, map[string]string{"scripts/check.sh": ""}, []string{"scripts/check.sh"}},
		{"source", []string{"make test"}, map[string]string{"widget.go": "package widget\n"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each case has its own repository and touches no environment.
			t.Parallel()
			root := project(t)
			for name, body := range parent {
				write(t, root, name, body)
			}
			commit(t, root, "start")
			for name, body := range tc.changes {
				if body == "" {
					if err := os.Remove(filepath.Join(root, filepath.FromSlash(name))); err != nil {
						t.Fatal(err)
					}
					continue
				}
				write(t, root, name, body)
			}
			got, err := setupChanged(context.Background(), root, tc.checks, nil)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("setupChanged = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// worked is a delegate that changes parse.go in its work tree and finishes.
func worked(t *testing.T) DelegateFunc {
	return func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		write(t, r.Options.WorkRoot, "parse.go", "package widget\n\nfunc Parse(line string) []string { return nil }\n")
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	}
}

// An interrupt while the checks run is not a failed check: the cell saves
// nothing, so the run keeps it running and reruns it.
func TestRunCellInterruptDuringChecks(t *testing.T) {
	f := newCellFixture(t)
	f.env.Delegate = worked(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.env.Checks = func(_ context.Context, _ string, commands []string) []journal.Check {
		// Ctrl-C reaches the checks as it reaches duolog: they exit non-zero.
		cancel()
		return []journal.Check{{Command: commands[0], Exit: 130}}
	}
	_, err := RunCell(ctx, f.env, f.cell, f.task, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunCell interrupted during the checks: err = %v, want context.Canceled", err)
	}
	if exists(f.cellFile("result.json")) {
		t.Fatal("an interrupted cell saved a result")
	}
	f.emptied(t, f.slot)
}

// A check that hangs is stopped at the check time cap and fails the cell
// with reason "check time cap", through the real check runner.
func TestRunCellCheckTimeCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep")
	}
	f := newCellFixture(t)
	f.env.Delegate = worked(t)
	f.env.Checks = profile.RunChecks
	class := f.env.Cfg.Learning.Classes["implement"]
	class.Checks = []string{"sleep 30"}
	f.env.Cfg.Learning.Classes["implement"] = class
	old := checkTimeout
	t.Cleanup(func() { checkTimeout = old })
	checkTimeout = func(config.Calibration) time.Duration { return 300 * time.Millisecond }
	started := time.Now()
	r := f.run(t)
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("the cell took %s; the check was not stopped", elapsed)
	}
	if r.State != CellDone || r.Passed || r.Reason != "check time cap" {
		t.Fatalf("result %+v, want done, not passed, check time cap", r)
	}
	if !exists(f.cellFile("checks.json")) {
		t.Fatal("checks.json is missing")
	}
	f.emptied(t, f.slot)
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
	if r.State != CellFailedLimit || r.Passed || r.Reason != "turn cap" {
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

// A worker whose reported usage grows past calibration.cell_max_tokens ends
// the cell as failed_limit with reason "token cap", keeps what it used and
// runs no checks; usage at the cap or below is not over it.
func TestRunCellTokenCap(t *testing.T) {
	for _, tc := range []struct {
		input, output int64
		over          bool
	}{
		{100, 50, false},
		{900, 100, false},
		{950, 50, false},
		{950, 51, true},
		{5000, 2000, true},
	} {
		f := newCellFixture(t)
		f.env.Cfg.Calibration.CellMaxTokens = 1000
		f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
			write(t, r.Options.WorkRoot, "parse.go", "package widget\n\nfunc Parse(line string) []string { return nil }\n")
			return delegate.Outcome{Result: runner.Result{Done: true, UsageKnown: true, Usage: runner.Usage{Input: tc.input, Output: tc.output}}}, nil
		}
		r := f.run(t)
		if r.Tokens != tc.input+tc.output {
			t.Fatalf("usage %d+%d: Tokens = %d", tc.input, tc.output, r.Tokens)
		}
		if tc.over {
			if r.State != CellFailedLimit || r.Passed || r.Reason != "token cap" {
				t.Fatalf("usage %d+%d: result %+v, want failed_limit, not passed, token cap", tc.input, tc.output, r)
			}
			if f.checkCalls != 0 {
				t.Fatalf("usage %d+%d: checks ran %d times after the token cap", tc.input, tc.output, f.checkCalls)
			}
		} else if r.State == CellFailedLimit || r.Reason == "token cap" {
			t.Fatalf("usage %d+%d: result %+v, want a cell within the cap", tc.input, tc.output, r)
		}
		f.emptied(t, f.slot)
	}
}

// A Claude cell's tree has the duolog gate active even when the project's
// gate is installed at project scope only and the replayed parent predates
// it: the worker runs with rules, not "sandbox only".
func TestRunCellClaudeTreeHasGate(t *testing.T) {
	f := newCellFixture(t)
	hooks, err := install.HooksJSON("claude", 150)
	if err != nil {
		t.Fatal(err)
	}
	// Installed after the parent and not committed.
	write(t, f.root, ".claude/settings.json", string(hooks))
	var out delegate.Outcome
	fake := f.realClaude(t, testbin.Scenario{Stdout: parseAnswer, Writes: map[string]string{"parse.go": "package widget\n\nfunc Parse(line string) []string { return nil }\n"}}, &out)
	r := f.run(t)
	if r.State != CellDone || !r.Passed {
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
	if r := f.run(t); r.State != CellDone {
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
	gate, err := install.HooksJSON("claude", 150)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		parent       map[string]string
		userSettings string
	}{
		{"project settings", map[string]string{".claude/settings.json": off}, ""},
		{"project settings with the gate", map[string]string{".claude/settings.json": `{"disableAllHooks":true,` + string(gate)[1:]}, ""},
		{"user settings", nil, off},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCellFixtureWith(t, tc.parent)
			if tc.userSettings != "" {
				write(t, filepath.Join(f.home, ".claude"), "settings.json", tc.userSettings)
			}
			var out delegate.Outcome
			fake := f.realClaude(t, testbin.Scenario{Stdout: parseAnswer}, &out)
			started := false
			_, err := RunCell(context.Background(), f.env, f.cell, f.task, func() (bool, error) { started = true; return true, nil })
			if err == nil || !strings.Contains(err.Error(), "disableAllHooks") || !strings.Contains(err.Error(), f.slot) {
				t.Fatalf("RunCell with hooks off = %v, want a refusal naming disableAllHooks and %s", err, f.slot)
			}
			if started {
				t.Fatal("a cell refused for hooks off was admitted; the refusal must come before start")
			}
			if exists(fake + ".args.json") {
				t.Fatalf("the worker ran with hooks off: %q", argv(t, fake))
			}
			if exists(f.cellFile("result.json")) {
				t.Fatal("a refused cell saved a result")
			}
		})
	}
}

func TestRunCellProviderLimit(t *testing.T) {
	f := newCellFixture(t)
	var tree string
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		tree = r.Options.WorkRoot
		return delegate.Outcome{Result: runner.Result{QuotaEnded: true, Usage: runner.Usage{Input: 10, Output: 5}}}, errors.New("usage limit reached")
	}
	r := f.run(t)
	if r.State != CellFailedLimit || r.Passed || r.Reason != "provider limit" {
		t.Fatalf("result %+v, want failed_limit, provider limit", r)
	}
	if r.Tokens != 15 {
		t.Fatalf("Tokens = %d, want 15", r.Tokens)
	}
	f.emptied(t, tree)
}

func TestRunCellLeakageExposed(t *testing.T) {
	f := newCellFixture(t)
	old := verifyIsolated
	t.Cleanup(func() { verifyIsolated = old })
	var tree string
	verifyIsolated = func(dir, repo string) error {
		tree = dir
		return errors.New("tree has a remote: origin")
	}
	r := f.run(t)
	if r.State != CellLeakage || r.Passed {
		t.Fatalf("result %+v, want leakage_exposed", r)
	}
	if f.checkCalls != 0 {
		t.Fatalf("checks ran %d times on an exposed tree", f.checkCalls)
	}
	f.emptied(t, tree)
}

// The first run with project-only Codex hooks: the refusal leaves the hooks
// the person must approve in the slot and nothing else, and once they are
// approved the next run works there.
func TestRunCellRefusesUntrustedTree(t *testing.T) {
	f := newCellFixture(t)
	trustCodex(t, f.home, f.slot, false, false)
	_, err := RunCell(context.Background(), f.env, f.cell, f.task, nil)
	if err == nil || !strings.Contains(err.Error(), f.slot) || !strings.Contains(err.Error(), "hooks") {
		t.Fatalf("RunCell on an untrusted tree: err = %v, want one naming %s and its hooks", err, f.slot)
	}
	onlyHooks(t, f.slot)
	if exists(f.cellFile("result.json")) {
		t.Fatal("a refused cell saved a result")
	}

	trustCodex(t, f.home, f.slot, true, true)
	f.env.Delegate = worked(t)
	if r := f.run(t); r.State != CellDone || !r.Passed {
		t.Fatalf("result %+v after approving the hooks, want done and passed", r)
	}
	onlyHooks(t, f.slot)
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

func TestRunCellTreeHasProjectHooks(t *testing.T) {
	f := newCellFixture(t)
	f.env.Delegate = func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		tree := r.Options.WorkRoot
		if body, err := os.ReadFile(filepath.Join(tree, ".codex", "hooks.json")); err != nil || string(body) != gateHooks {
			t.Errorf("hooks.json in the work tree = %q, %v", body, err)
		}
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	}
	f.run(t)
}

func TestCellBriefDemotesStatementHeadings(t *testing.T) {
	f := newCellFixture(t)
	f.task.Statement = "Add Parse.\n\n## Background\n\nLines come from a file.\n\n## Check\n\nIt splits.\n\n```\n## kept in the fence\n```\n"
	var brief string
	var invalid error
	f.env.Delegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		taskPath := r.TaskPath
		data, err := os.ReadFile(taskPath)
		if err != nil {
			t.Fatal(err)
		}
		brief, invalid = string(data), taskspec.Validate(os.DirFS(filepath.Dir(taskPath)), filepath.Base(taskPath))
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	}
	f.run(t)
	if invalid != nil {
		t.Fatalf("brief is not a valid task: %v\n%s", invalid, brief)
	}
	for _, want := range []string{"\n### Background\n", "\n### Check\n\nIt splits.", "\n## kept in the fence\n"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief lacks %q:\n%s", want, brief)
		}
	}
}

func TestRunCellBriefHasNoSolution(t *testing.T) {
	f := newCellFixture(t)
	var brief string
	f.env.Delegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		data, err := os.ReadFile(r.TaskPath)
		if err != nil {
			t.Fatal(err)
		}
		brief = string(data)
		return delegate.Outcome{Result: runner.Result{Done: true}}, nil
	}
	f.run(t)
	if !strings.Contains(brief, parseStatement) {
		t.Fatalf("brief lacks the statement:\n%s", brief)
	}
	for _, secret := range []string{f.task.Ref[:7], f.task.Parent[:7], f.task.Subject} {
		if strings.Contains(brief, secret) {
			t.Fatalf("brief holds %q:\n%s", secret, brief)
		}
	}
	reference, err := os.ReadFile(filepath.Join(f.runDir, TasksDir, f.task.ID, "reference.patch"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(reference), "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		changed := strings.TrimSpace(strings.TrimLeft(line, "+-"))
		if (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")) && len(changed) > 2 && strings.Contains(brief, changed) {
			t.Fatalf("brief holds the reference line %q:\n%s", changed, brief)
		}
	}
	if !strings.Contains(brief, "Finish within 45 minutes and 100 tool turns. Work only in the work tree. Do not search for the history of this code.") {
		t.Fatalf("brief lacks the limits:\n%s", brief)
	}
}

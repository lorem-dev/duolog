package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	corerun "github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

// submoduleDelegateStart records the detached starts of one test.
type submoduleDelegateStart struct {
	argv     []string
	dir      string
	starts   int
	failNext bool
}

func TestMCPOwnProjectNeedsNoDelegationLocation(t *testing.T) {
	d, f, _, _ := submoduleDelegateProject(t)
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Wait: &no})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(project.LeadHome(f.Superproject))
	if err != nil {
		t.Fatal(err)
	}
	locations, err := reg.Delegations("")
	if err != nil || len(locations) != 0 {
		t.Fatalf("own project locations: %+v (%v)", locations, err)
	}
	for _, root := range []string{f.Superproject, f.Submodule} {
		records, err := leadDelegationRecords(reg, root, "lead-mcp")
		if err != nil || len(records) != 1 || records[0].ID != out.ID {
			t.Fatalf("own project record from %s: %+v (%v)", root, records, err)
		}
		if _, ok, err := mcpResultByID(reg, root, out.ID); err != nil || !ok {
			t.Fatalf("own project result from %s: %v %v", root, ok, err)
		}
	}
}

// submoduleDelegateProject is a superproject with a configured submodule, a
// named linked checkout of the submodule and a live lead in this process,
// served from the superproject.
func submoduleDelegateProject(t *testing.T) (*cmdDeps, submodule.Fixture, string, *submoduleDelegateStart) {
	t.Helper()
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	t.Setenv("DUOLOG_PEER", "")
	f := submodule.New(t)
	submoduleFile(t, f.Superproject, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	submoduleFile(t, f.Submodule, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	feature := filepath.Join(f.Submodule, ".worktrees", "feature")
	submodule.Git(t, f.Submodule, "worktree", "add", "-q", "--detach", feature)
	t.Chdir(f.Superproject)
	reg, err := session.Open(project.LeadHome(f.Superproject))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-mcp", Target: config.Target{Agent: "claude"}, PID: os.Getpid(), Root: f.Superproject}); err != nil {
		t.Fatal(err)
	}
	d := testDeps(t)
	start := &submoduleDelegateStart{}
	d.startDetached = func(argv []string, dir, _ string) error {
		if start.failNext {
			start.failNext = false
			return errors.New("no process")
		}
		start.argv, start.dir = argv, dir
		start.starts++
		return nil
	}
	return d, f, feature, start
}

// finishRecord writes the outcome of the run whose record is at path.
func finishRecord(t *testing.T, path string, out delegate.Outcome) {
	t.Helper()
	r, err := delegate.ReadRecord(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := delegate.WriteRecord(path, delegate.RecordFor(r, out, nil, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
}

func underDir(t *testing.T, path, dir string) bool {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	full, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.HasPrefix(full, resolved+string(filepath.Separator))
}

func TestMCPSubmoduleDelegateResults(t *testing.T) {
	d, f, feature, start := submoduleDelegateProject(t)
	ctx := context.Background()
	first, err := d.mcpDelegate(ctx, mcp.DelegateInput{Role: "implement", Task: testTask, Worktree: f.Linked, Wait: &no})
	if err != nil {
		t.Fatal(err)
	}
	childDir, recordPath := start.dir, first.AnswerPath
	if childDir != f.Linked {
		t.Fatal(childDir)
	}
	if !underDir(t, recordPath, filepath.Join(f.Submodule, ".duolog", "delegations")) {
		t.Fatal(recordPath)
	}
	if i := slices.Index(start.argv, "--lead"); i < 0 || start.argv[i+1] != "lead-mcp" {
		t.Fatalf("lead not bound: %q", start.argv)
	}
	if _, err := os.Stat(filepath.Join(f.Linked, filepath.FromSlash(first.TaskPath))); err != nil {
		t.Fatalf("task file not in the submodule checkout: %v", err)
	}
	retry, err := d.mcpDelegate(ctx, mcp.DelegateInput{Role: "implement", Task: testTask, Worktree: f.Linked, Wait: &no})
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := d.mcpDelegate(ctx, mcp.DelegateInput{Role: "implement", Task: testTask, Wait: &no})
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != first.ID || otherProject.ID == first.ID {
		t.Fatal("wrong twin")
	}
	if start.starts != 2 || start.dir != f.Superproject {
		t.Fatalf("%d starts, last in %s", start.starts, start.dir)
	}
	if !underDir(t, otherProject.AnswerPath, filepath.Join(f.Superproject, ".duolog", "delegations")) {
		t.Fatal(otherProject.AnswerPath)
	}

	finishRecord(t, recordPath, delegate.Outcome{Line: "done=true", Result: corerun.Result{Done: true, Summary: "submodule result"}})
	result, err := d.mcpDelegate(ctx, mcp.DelegateInput{Action: "result", TaskPath: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "submodule result" || result.AnswerPath != recordPath {
		t.Fatal(result)
	}
	status, err := d.mcpStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	statusRow := delegationRow(t, status.Delegations, recordPath)
	if statusRow.AnswerPath != recordPath {
		t.Fatal(statusRow)
	}
	if statusRow.WorkRoot != f.Linked {
		t.Fatal(statusRow.WorkRoot)
	}
	if row := delegationRow(t, status.Delegations, otherProject.AnswerPath); row.WorkRoot != f.Superproject {
		t.Fatal(row)
	}

	t.Run("bare name, run here and a removed worktree", func(t *testing.T) {
		named, err := d.mcpDelegate(ctx, mcp.DelegateInput{Role: "review", Task: testTask, Worktree: "feature", Wait: &no})
		if err != nil {
			t.Fatal(err)
		}
		namedDir := start.dir
		if !sameDir(t, namedDir, feature) {
			t.Fatal(namedDir)
		}
		p := delegate.Placement{Where: "self", Task: named.TaskPath}
		finishRecord(t, named.AnswerPath, delegate.Outcome{Line: p.String(), RunHere: &p})
		submodule.Git(t, f.Submodule, "worktree", "remove", "--force", feature)
		got, err := d.mcpDelegate(ctx, mcp.DelegateInput{Action: "result", TaskPath: named.ID})
		if err != nil || got.State != "run_here" || got.RunHere == nil || got.AnswerPath != named.AnswerPath {
			t.Fatalf("%+v %v", got, err)
		}
		status, err := d.mcpStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if row := delegationRow(t, status.Delegations, named.AnswerPath); row.WorkRoot != namedDir {
			t.Fatal(row)
		}
	})

	t.Run("failed launch", func(t *testing.T) {
		start.failNext = true
		other := *testTask
		other.Goal = "Fix the lexer."
		if _, err := d.mcpDelegate(ctx, mcp.DelegateInput{Role: "implement", Task: &other, Worktree: f.Submodule, Wait: &no}); err == nil {
			t.Fatal("failed start reported success")
		}
		status, err := d.mcpStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		failed := 0
		for _, row := range status.Delegations {
			if row.State == "failed" && row.WorkRoot == f.Submodule && underDir(t, row.AnswerPath, filepath.Join(f.Submodule, ".duolog", "delegations")) {
				failed++
			}
		}
		if failed != 1 {
			t.Fatalf("failed start not listed in its project: %+v", status.Delegations)
		}
	})

	t.Run("colliding relative task paths", func(t *testing.T) {
		testCollidingTaskPaths(t, d, f)
	})
}

func delegationRow(t *testing.T, rows []mcp.Delegation, answerPath string) mcp.Delegation {
	t.Helper()
	for _, row := range rows {
		if row.AnswerPath == answerPath {
			return row
		}
	}
	t.Fatalf("no status row for %s in %+v", answerPath, rows)
	return mcp.Delegation{}
}

// testCollidingTaskPaths records two runs whose relative task paths are the
// same in the superproject and the submodule: a relative path then names no
// one run, and the worktree or the id picks one.
func testCollidingTaskPaths(t *testing.T, d *cmdDeps, f submodule.Fixture) {
	t.Helper()
	ctx := context.Background()
	reg, err := session.Open(project.LeadHome(f.Superproject))
	if err != nil {
		t.Fatal(err)
	}
	shared := ".duolog/tasks/shared/task.md"
	paths := map[string]string{}
	for id, root := range map[string]string{"d-super1": f.Superproject, "d-subm01": f.Submodule} {
		r := delegate.Record{ID: id, Role: "implement", Lead: "lead-mcp", TaskPath: shared, WorkRoot: root, State: "done", Summary: id, Started: time.Now().UTC()}
		paths[id] = filepath.Join(delegate.RecordDir(filepath.Join(root, ".duolog")), id+".json")
		if err := delegate.WriteRecord(paths[id], r); err != nil {
			t.Fatal(err)
		}
		if err := reg.RecordDelegation(session.DelegationLocation{ID: id, Lead: "lead-mcp", WorkRoot: root}); err != nil {
			t.Fatal(err)
		}
	}
	_, ambiguousErr := d.mcpDelegate(ctx, mcp.DelegateInput{Action: "result", TaskPath: shared})
	if ambiguousErr == nil {
		t.Fatal("ambiguous relative task path")
	}
	got, err := d.mcpDelegate(ctx, mcp.DelegateInput{Action: "result", TaskPath: shared, Worktree: f.Submodule})
	if err != nil || got.Summary != "d-subm01" || !sameDir(t, got.AnswerPath, paths["d-subm01"]) {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = d.mcpDelegate(ctx, mcp.DelegateInput{Action: "result", TaskPath: "d-super1"})
	if err != nil || got.Summary != "d-super1" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = d.mcpDelegate(ctx, mcp.DelegateInput{Action: "result", TaskPath: filepath.Join(f.Submodule, filepath.FromSlash(shared))})
	if err != nil || got.Summary != "d-subm01" {
		t.Fatalf("absolute task path: %+v %v", got, err)
	}
	// A location whose record is not readable is an error, not a silent miss.
	if err := os.WriteFile(paths["d-subm01"], []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.mcpDelegate(ctx, mcp.DelegateInput{Action: "result", TaskPath: "d-subm01"}); err == nil {
		t.Fatal("malformed record accepted")
	}
}

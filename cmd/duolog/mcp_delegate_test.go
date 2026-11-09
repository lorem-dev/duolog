package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/policy"
	corerun "github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
	"github.com/lorem-dev/duolog/internal/store"
)

// mcpDelegateProject is a duolog project in a temporary folder, the working
// directory, with a live lead in this process. It returns the commands'
// dependencies, the project root and the state dir.
func mcpDelegateProject(t *testing.T) (*cmdDeps, string, string) {
	t.Helper()
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	t.Setenv("DUOLOG_PEER", "")
	root := t.TempDir()
	t.Chdir(root)
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-mcp", Target: config.Target{Agent: "claude"}, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	d := testDeps(t)
	d.startDetached = func([]string, string, string) error {
		t.Fatal("unexpected start")
		return nil
	}
	return d, root, s.Dir
}

// startInProcess replaces the detached start with a run of the same argv
// through the root command in this process, with its output in the log. It
// returns the argv of the last start.
func startInProcess(t *testing.T, d *cmdDeps) *[]string {
	t.Helper()
	var got []string
	d.startDetached = func(argv []string, dir, logPath string) error {
		got = argv
		var log bytes.Buffer
		cmd := newRootCommand(d)
		cmd.SetOut(&log)
		cmd.SetErr(&log)
		cmd.SetArgs(argv[1:])
		_ = cmd.Execute()
		return os.WriteFile(logPath, log.Bytes(), 0o600)
	}
	return &got
}

// shortWait makes the poll and a minute of the wait short for one test.
func shortWait(t *testing.T, minute time.Duration) {
	t.Helper()
	poll, unit := mcpDelegatePoll, mcpDelegateMinute
	mcpDelegatePoll, mcpDelegateMinute = time.Millisecond, minute
	t.Cleanup(func() { mcpDelegatePoll, mcpDelegateMinute = poll, unit })
}

var no = false

var testTask = &mcp.Task{Goal: "Fix the parser.", Read: []string{"parse.go"}, Write: []string{"parse.go"}, Limits: []string{"small"}, Check: []string{"go test ./..."}}

func TestMCPDelegateWritesTaskAndStartsDetached(t *testing.T) {
	d, root, stateDir := mcpDelegateProject(t)
	var argv []string
	var dir, logPath string
	d.startDetached = func(a []string, wd, log string) error {
		argv, dir, logPath = a, wd, log
		return nil
	}
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Use: "codex/sol/high", Needs: []string{"skill:a", "mcp:b"}, Wait: &no})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\.duolog/tasks/d-[a-z2-7]{6}/task\.md$`).MatchString(out.TaskPath) {
		t.Fatalf("task path %q", out.TaskPath)
	}
	id := strings.Split(out.TaskPath, "/")[2]
	record := filepath.Join(delegate.RecordDir(stateDir), id+".json")
	if out.State != "running" || out.AnswerPath != record || out.Answer != "" || out.ID != id {
		t.Fatalf("%+v", out)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{exe, "delegate", "implement", "--task", out.TaskPath, "--lead", "lead-mcp", "--result-file", record, "--use", "codex/sol/high", "--needs", "skill:a,mcp:b"}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %q\nwant  %q", argv, want)
	}
	if !sameDir(t, dir, root) || logPath != filepath.Join(delegate.RecordDir(stateDir), id+".log") {
		t.Fatal(dir, logPath)
	}
	if err := taskspec.Validate(os.DirFS(root), out.TaskPath); err != nil {
		t.Fatalf("task file: %v", err)
	}
	r, err := delegate.ReadRecord(record, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The server leaves the PID to the child, which writes it at its start.
	if r.ID != id || r.State != "running" || r.PID != 0 || r.Lead != "lead-mcp" || r.Role != "implement" || r.TaskPath != out.TaskPath || !sameDir(t, r.WorkRoot, root) || r.Started.IsZero() {
		t.Fatalf("%+v", r)
	}
}

func TestMCPDelegateWaitsByDefaultAndReturnsTheWholeResult(t *testing.T) {
	d, _, _ := mcpDelegateProject(t)
	shortWait(t, time.Minute)
	startInProcess(t, d)
	res := corerun.Result{Done: true, Summary: "Fixed.", Files: []string{"parse.go"}, Decisions: []string{"kept the name"}}
	d.RunDelegate = func(context.Context, delegate.Request) (delegate.Outcome, error) {
		return delegate.Outcome{Line: "done=true summary=Fixed.", Result: res}, nil
	}
	// No wait field: the call waits.
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask})
	if err != nil || out.State != "done" || out.Answer != "done=true summary=Fixed." || out.AnswerPath == "" || out.TaskPath == "" {
		t.Fatalf("%+v %v", out, err)
	}
	if out.Done == nil || !*out.Done || out.Summary != "Fixed." || !slices.Equal(out.Files, res.Files) || !slices.Equal(out.Decisions, res.Decisions) || out.ID == "" {
		t.Fatalf("the response lacks the result: %+v", out)
	}
	// The record keeps the answer: the server did not write its running
	// record with the PID over it.
	r, err := delegate.ReadRecord(out.AnswerPath, nil)
	if err != nil || r.State != "done" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestMCPDelegateWaitTimesOutRunning(t *testing.T) {
	d, _, _ := mcpDelegateProject(t)
	shortWait(t, 20*time.Millisecond)
	d.startDetached = func([]string, string, string) error { return nil }
	start := time.Now()
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, TimeoutMinutes: 1})
	if err != nil || out.State != "running" || out.TaskPath == "" || out.AnswerPath == "" || out.ID == "" || out.Done != nil {
		t.Fatalf("%+v %v", out, err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond || elapsed > 5*time.Second {
		t.Fatal(elapsed)
	}
	// A context that ends stops the wait too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	shortWait(t, time.Minute)
	out, err = d.mcpDelegate(ctx, mcp.DelegateInput{Role: "implement", Task: testTask})
	if err != nil || out.State != "running" {
		t.Fatalf("%+v %v", out, err)
	}
}

// A run that outlasts the bound answers running with its id, and calling again
// with that id keeps waiting for the same run and returns its result.
func TestMCPDelegateRunningCallContinuesByID(t *testing.T) {
	d, _, stateDir := mcpDelegateProject(t)
	shortWait(t, 20*time.Millisecond)
	d.startDetached = func([]string, string, string) error { return nil }
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask})
	if err != nil || out.State != "running" || out.ID == "" {
		t.Fatalf("%+v %v", out, err)
	}
	path := filepath.Join(delegate.RecordDir(stateDir), out.ID+".json")
	r, err := delegate.ReadRecord(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r = delegate.RecordFor(r, delegate.Outcome{Line: "done=true", Result: corerun.Result{Done: true, Summary: "Late."}}, nil, time.Now())
	if err := delegate.WriteRecord(path, r); err != nil {
		t.Fatal(err)
	}
	for _, in := range []mcp.DelegateInput{{Action: "result", TaskPath: out.ID}, {Action: "result", TaskPath: out.ID, Wait: new(bool)}} {
		again, err := d.mcpDelegate(context.Background(), in)
		if err != nil || again.State != "done" || again.Summary != "Late." || again.ID != out.ID {
			t.Fatalf("%+v %v", again, err)
		}
	}
}

// A client that gave up before the reply calls again with the same task: it
// gets the run that still runs, with its id, and no second worker starts.
func TestMCPDelegateSameTaskWhileRunningReturnsThatRun(t *testing.T) {
	d, _, _ := mcpDelegateProject(t)
	starts := 0
	d.startDetached = func([]string, string, string) error { starts++; return nil }
	first, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Wait: &no})
	if err != nil || first.State != "running" || first.ID == "" {
		t.Fatalf("%+v %v", first, err)
	}
	again, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Wait: &no})
	if err != nil || again.State != "running" || again.ID != first.ID || again.TaskPath != first.TaskPath || again.AnswerPath != first.AnswerPath {
		t.Fatalf("again = %+v %v, want the run %+v", again, err, first)
	}
	if starts != 1 {
		t.Fatalf("%d workers started, want 1", starts)
	}
	// Another task, or another role, is another run.
	other := *testTask
	other.Goal = "Fix the lexer."
	for _, in := range []mcp.DelegateInput{{Role: "implement", Task: &other, Wait: &no}, {Role: "tests", Task: testTask, Wait: &no}} {
		out, err := d.mcpDelegate(context.Background(), in)
		if err != nil || out.ID == first.ID {
			t.Fatalf("%+v: %+v %v", in, out, err)
		}
	}
	if starts != 3 {
		t.Fatalf("%d workers started, want 3", starts)
	}
}

// Calls that differ in use, needs, force or skip_preflight are different runs.
func TestMCPDelegateSameTaskWithOtherOptionsIsAnotherRun(t *testing.T) {
	d, _, _ := mcpDelegateProject(t)
	starts := 0
	d.startDetached = func([]string, string, string) error { starts++; return nil }
	base := mcp.DelegateInput{Role: "implement", Task: testTask, Wait: &no, Use: "codex/sol/medium", Needs: []string{"mcp:a", "skill:b"}}
	first, err := d.mcpDelegate(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	same := base
	same.Needs = []string{"skill:b", "mcp:a"}
	if again, err := d.mcpDelegate(context.Background(), same); err != nil || again.ID != first.ID {
		t.Fatalf("same options: %+v %v, want the run %s", again, err, first.ID)
	}
	variants := map[string]func(*mcp.DelegateInput){
		"use":            func(in *mcp.DelegateInput) { in.Use = "claude/haiku/low" },
		"needs":          func(in *mcp.DelegateInput) { in.Needs = nil },
		"force":          func(in *mcp.DelegateInput) { in.Force = true },
		"skip_preflight": func(in *mcp.DelegateInput) { in.SkipPreflight = true },
	}
	want := 1
	for name, change := range variants {
		in := base
		change(&in)
		out, err := d.mcpDelegate(context.Background(), in)
		want++
		if err != nil || out.ID == first.ID || starts != want {
			t.Fatalf("%s: %+v %v, %d starts, want %d", name, out, err, starts, want)
		}
	}
}

// Once the run ended, the same task starts a new run.
func TestMCPDelegateSameTaskAfterTheRunEndedStartsAgain(t *testing.T) {
	d, _, stateDir := mcpDelegateProject(t)
	starts := 0
	d.startDetached = func([]string, string, string) error { starts++; return nil }
	first, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Wait: &no})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(delegate.RecordDir(stateDir), first.ID+".json")
	r, err := delegate.ReadRecord(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := delegate.WriteRecord(path, delegate.RecordFor(r, delegate.Outcome{Line: "done=true"}, nil, time.Now())); err != nil {
		t.Fatal(err)
	}
	again, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Wait: &no})
	if err != nil || again.ID == first.ID || starts != 2 {
		t.Fatalf("%+v %v, %d starts", again, err, starts)
	}
}

// A refused preflight comes back as data: the blocked commands, with remember
// on an ask only, and the Check lines that were not read.
func TestMCPDelegateOutputBlockedRememberAndSkippedChecks(t *testing.T) {
	r := delegate.Record{ID: "d-bbbbbb", State: "failed", Line: "preflight: no worker started",
		Blocked: []policy.Blocked{
			{Command: "go run ./cmd/x", Decision: policy.Ask, Reason: "runs any program"},
			{Command: "uv run git push", Decision: policy.Deny, Reason: "denied"},
		},
		Skipped: []string{"see the Makefile"}}
	out := mcpDelegateOutput("p.json", r)
	want := []mcp.BlockedCheck{
		{Command: "go run ./cmd/x", Decision: "ask", Reason: "runs any program", Remember: "duolog remember 'go run ./cmd/x'"},
		{Command: "uv run git push", Decision: "deny", Reason: "denied"},
	}
	if !reflect.DeepEqual(out.Blocked, want) {
		t.Errorf("blocked = %+v, want %+v", out.Blocked, want)
	}
	if !slices.Equal(out.SkippedChecks, []string{"see the Makefile"}) {
		t.Errorf("skipped_checks = %q", out.SkippedChecks)
	}
	// A run that started returns them too.
	done := delegate.Record{ID: "d-cccccc", State: "done", Skipped: []string{"run it by hand"}}
	if out := mcpDelegateOutput("p.json", done); len(out.Blocked) != 0 || !slices.Equal(out.SkippedChecks, []string{"run it by hand"}) {
		t.Errorf("done run: %+v", out)
	}
}

func TestMCPDelegateResultWaitsForARunningRun(t *testing.T) {
	d, _, stateDir := mcpDelegateProject(t)
	shortWait(t, 20*time.Millisecond)
	base := delegate.Record{ID: "d-wwwwww", Role: "implement", TaskPath: ".duolog/tasks/d-wwwwww/task.md", State: "running", Started: time.Now()}
	path := filepath.Join(delegate.RecordDir(stateDir), base.ID+".json")
	if err := delegate.WriteRecord(path, base); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(5 * time.Millisecond)
		_ = delegate.WriteRecord(path, delegate.RecordFor(base, delegate.Outcome{Result: corerun.Result{Done: true, Summary: "Now."}}, nil, time.Now()))
	}()
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Action: "result", TaskPath: "d-wwwwww", Wait: nil})
	if err != nil || out.State != "done" || out.Summary != "Now." {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestMCPDelegateWaitFalseReturnsAtOnce(t *testing.T) {
	d, _, _ := mcpDelegateProject(t)
	d.startDetached = func([]string, string, string) error { return nil }
	start := time.Now()
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Wait: &no})
	if err != nil || out.State != "running" || out.ID == "" || time.Since(start) > 5*time.Second {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestMCPDelegateDiffstat(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "a.go")
	git("commit", "-q", "-m", "first")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("one\nthree\nfour\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := mcpDiffstat(root, []string{"a.go", "b.go", "c.go"})
	if !strings.Contains(got, "1 file changed, 2 insertions(+), 1 deletion(-)") || !strings.Contains(got, "new: b.go") || strings.Contains(got, "c.go") {
		t.Fatalf("diffstat %q", got)
	}
	if got := mcpDiffstat(root, nil); got != "" {
		t.Fatalf("no files: %q", got)
	}
	if got := mcpDiffstat(t.TempDir(), []string{"a.go"}); got != "" {
		t.Fatalf("not a repository: %q", got)
	}
}

func TestMCPDelegateWaitCappedAtMax(t *testing.T) {
	for minutes, want := range map[int]int{60: mcp.MaxWaitMinutes, 0: 10, -1: 10, 3: 3, 10: 10, 11: 10} {
		if got := mcpDelegateTimeout(minutes); got != time.Duration(want)*time.Minute {
			t.Errorf("timeout(%d) = %v, want %d minutes", minutes, got, want)
		}
	}
}

func TestMCPDelegateResultByTaskPath(t *testing.T) {
	d, root, stateDir := mcpDelegateProject(t)
	dir := delegate.RecordDir(stateDir)
	p := delegate.Placement{Where: "subagent", Model: "sonnet", Effort: "high", Task: ".duolog/tasks/d-aaaaaa/task.md"}
	records := []delegate.Record{
		{ID: "d-aaaaaa", Role: "implement", TaskPath: ".duolog/tasks/d-aaaaaa/task.md", WorkRoot: root, State: "run_here", Line: p.String(), RunHere: &p, Started: time.Unix(1, 0)},
		{ID: "d-bbbbbb", Role: "review", TaskPath: ".duolog/tasks/d-bbbbbb/task.md", WorkRoot: root, State: "needs", Line: "needs: mcp:figma", Needs: []string{"mcp:figma"}, Started: time.Unix(2, 0)},
		{ID: "d-cccccc", Role: "implement", TaskPath: ".duolog/tasks/d-cccccc/task.md", WorkRoot: root, State: "done", Line: "done=true", Started: time.Unix(3, 0)},
	}
	for _, r := range records {
		if err := delegate.WriteRecord(filepath.Join(dir, r.ID+".json"), r); err != nil {
			t.Fatal(err)
		}
	}
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Action: "result", TaskPath: ".duolog/tasks/d-aaaaaa/task.md"})
	want := mcp.DelegateOutput{State: "run_here", TaskPath: ".duolog/tasks/d-aaaaaa/task.md", Answer: p.String(), AnswerPath: filepath.Join(dir, "d-aaaaaa.json"), RunHere: &mcp.RunHere{Mode: "subagent", Model: "sonnet", Effort: "high", Task: p.Task}}
	if err != nil || out.RunHere == nil || *out.RunHere != *want.RunHere {
		t.Fatalf("%+v %v", out, err)
	}
	out.RunHere, want.RunHere = nil, nil
	if out.State != want.State || out.TaskPath != want.TaskPath || out.Answer != want.Answer || out.AnswerPath != want.AnswerPath || out.Needs != nil {
		t.Fatalf("%+v", out)
	}
	// By id, and by the task file's full path.
	for _, key := range []string{"d-bbbbbb", filepath.Join(root, ".duolog", "tasks", "d-bbbbbb", "task.md")} {
		out, err = d.mcpDelegate(context.Background(), mcp.DelegateInput{Action: "result", TaskPath: key})
		if err != nil || out.State != "needs" || !slices.Equal(out.Needs, []string{"mcp:figma"}) || out.Answer != "needs: mcp:figma" || out.RunHere != nil {
			t.Fatalf("%s: %+v %v", key, out, err)
		}
	}
	_, err = d.mcpDelegate(context.Background(), mcp.DelegateInput{Action: "result", TaskPath: ".duolog/tasks/d-zzzzzz/task.md"})
	if err == nil || err.Error() != "no delegation with task_path .duolog/tasks/d-zzzzzz/task.md; status lists them" {
		t.Fatal(err)
	}
}

func TestMCPDelegateRefusesWithoutTask(t *testing.T) {
	d, _, stateDir := mcpDelegateProject(t)
	const want = "delegate needs role and task; for an earlier run use action=result with its task_path"
	for _, in := range []mcp.DelegateInput{{Role: "implement"}, {Task: testTask}, {Action: "run", Role: "implement"}} {
		if _, err := d.mcpDelegate(context.Background(), in); err == nil || err.Error() != want {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	if _, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Action: "rerun", Role: "implement", Task: testTask}); err == nil || !strings.Contains(err.Error(), "run or result") {
		t.Fatal(err)
	}
	if _, err := os.Stat(delegate.RecordDir(stateDir)); !os.IsNotExist(err) {
		t.Fatal("a refused call wrote a record", err)
	}
}

func TestMCPDelegateRoundTripsThroughRunDelegate(t *testing.T) {
	d, root, _ := mcpDelegateProject(t)
	shortWait(t, time.Minute)
	startInProcess(t, d)
	var got []delegate.Request
	d.RunDelegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		got = append(got, r)
		return delegate.Outcome{Line: "done=true"}, nil
	}
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Use: "codex/sol/high", Needs: []string{"skill:a", "mcp:b"}})
	if err != nil || out.State != "done" {
		t.Fatalf("%+v %v", out, err)
	}
	cli, err := executeBusCommand(t, "delegate", "implement", "--task", out.TaskPath, "--lead", "lead-mcp", "--use", "codex/sol/high", "--needs", "skill:a,mcp:b")
	if err != nil || cli != "done=true\n" || len(got) != 2 {
		t.Fatal(cli, err, len(got))
	}
	server, direct := got[0], got[1]
	if server.Role != direct.Role || server.TaskPath != direct.TaskPath || server.Thread != direct.Thread || server.StateDir != direct.StateDir || !reflect.DeepEqual(server.Config, direct.Config) {
		t.Fatalf("server %+v\ndirect %+v", server, direct)
	}
	so, do := server.Options, direct.Options
	if so.Lead != do.Lead || so.Lead != "lead-mcp" || !slices.Equal(so.Needs, do.Needs) || so.Entry == nil || do.Entry == nil || *so.Entry != *do.Entry || so.Person != do.Person || !slices.Equal(so.ProtectedRoots, do.ProtectedRoots) {
		t.Fatalf("server %+v\ndirect %+v", so, do)
	}
	if !sameDir(t, filepath.Dir(server.StateDir), root) {
		t.Fatal(server.StateDir, root)
	}
}

// sameDir reports whether a and b name the same folder.
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

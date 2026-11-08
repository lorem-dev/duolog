package main

import (
	"bytes"
	"context"
	"os"
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

var testTask = &mcp.Task{Goal: "Fix the parser.", Read: []string{"parse.go"}, Write: []string{"parse.go"}, Limits: []string{"small"}, Check: []string{"go test ./..."}}

func TestMCPDelegateWritesTaskAndStartsDetached(t *testing.T) {
	d, root, stateDir := mcpDelegateProject(t)
	var argv []string
	var dir, logPath string
	d.startDetached = func(a []string, wd, log string) error {
		argv, dir, logPath = a, wd, log
		return nil
	}
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Use: "codex/sol/high", Needs: []string{"skill:a", "mcp:b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\.duolog/tasks/d-[a-z2-7]{6}/task\.md$`).MatchString(out.TaskPath) {
		t.Fatalf("task path %q", out.TaskPath)
	}
	id := strings.Split(out.TaskPath, "/")[2]
	record := filepath.Join(delegate.RecordDir(stateDir), id+".json")
	if out.State != "running" || out.AnswerPath != record || out.Answer != "" {
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

func TestMCPDelegateWaitReturnsAnswer(t *testing.T) {
	d, _, _ := mcpDelegateProject(t)
	shortWait(t, time.Minute)
	startInProcess(t, d)
	d.RunDelegate = func(context.Context, delegate.Request) (delegate.Outcome, error) {
		return delegate.Outcome{Line: "done=true summary=Fixed."}, nil
	}
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Wait: true})
	if err != nil || out.State != "done" || out.Answer != "done=true summary=Fixed." || out.AnswerPath == "" || out.TaskPath == "" {
		t.Fatalf("%+v %v", out, err)
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
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Wait: true, TimeoutMinutes: 1})
	if err != nil || out.State != "running" || out.TaskPath == "" || out.AnswerPath == "" {
		t.Fatalf("%+v %v", out, err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond || elapsed > 5*time.Second {
		t.Fatal(elapsed)
	}
	// A context that ends stops the wait too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	shortWait(t, time.Minute)
	out, err = d.mcpDelegate(ctx, mcp.DelegateInput{Role: "implement", Task: testTask, Wait: true})
	if err != nil || out.State != "running" {
		t.Fatalf("%+v %v", out, err)
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
	out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "implement", Task: testTask, Use: "codex/sol/high", Needs: []string{"skill:a", "mcp:b"}, Wait: true})
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

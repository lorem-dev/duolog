package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestDelegateCommandOnlyPrintsAnswer(t *testing.T) {
	isolatedRouteConfig(t)
	claudeGateRuns(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	root := t.TempDir()
	t.Chdir(root)
	taskPath := filepath.Join(root, "task.md")
	for p, s := range map[string]string{taskPath: "# Task: command\n\n## Goal\nDo work.\n\n## Read\n- a.go\n\n## Write\nNone.\n\n## Limits\nNo changes.\n\n## Check\nReview.\n", filepath.Join(root, "a.go"): "PRIVATE_COMMAND_CONTENT\n"} {
		if e := os.WriteFile(p, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	fake := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"structured_output":{"done":true,"summary":"Reviewed.","files":["a.go"],"needs":[]}}`})
	s, e := store.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	reg, e := session.Open(s.Dir)
	if e != nil {
		t.Fatal(e)
	}
	if e := reg.RegisterLead(session.Lead{ID: "registered-lead", Target: config.Target{Agent: "codex", Model: "sol", Effort: "medium"}, Mode: mode.Plan}); e != nil {
		t.Fatal(e)
	}
	out, e := executeBusCommand(t, "delegate", "review_final", "--task", taskPath, "--lead", "registered-lead", "--thread", "topic")
	if e != nil || strings.Count(out, "\n") != 1 || !strings.Contains(out, "done=true") || !strings.Contains(out, "Reviewed.") || !strings.Contains(out, "a.go") || strings.Contains(out, "PRIVATE_COMMAND_CONTENT") {
		t.Fatal(out, e)
	}
	data, e := os.ReadFile(fake + ".env.json")
	if e != nil || !strings.Contains(string(data), "DUOLOG_LEAD=registered-lead") {
		t.Fatal(string(data), e)
	}
	data, e = os.ReadFile(filepath.Join(s.Dir, "bus.ndjson"))
	if e != nil || strings.Contains(string(data), "PRIVATE_COMMAND_CONTENT") {
		t.Fatal(string(data), e)
	}
	var line map[string]any
	if e := json.Unmarshal([]byte(strings.Split(string(data), "\n")[0]), &line); e != nil || line["from"] != "codex/lead.regist" {
		t.Fatal(line, e)
	}
}
func TestDelegateCommandRefusalIsOneLine(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "1")
	out, e := executeBusCommand(t, "delegate", "implement", "--task", "task.md")
	if e == nil || strings.Count(out, "\n") != 1 || !strings.Contains(out, "nested") {
		t.Fatal(out, e)
	}
}

func TestDelegateRejectsBadFeatures(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "")
	ran := false
	testDeps(t).RunDelegate = func(context.Context, delegate.Request) (delegate.Outcome, error) {
		ran = true
		return delegate.Outcome{}, nil
	}
	out, e := executeBusCommand(t, "delegate", "debug_hard", "--task", "task.md", "--features", "output=poem")
	if e == nil || ran || strings.Count(out, "\n") != 1 || !strings.Contains(out, "diagnosis") {
		t.Fatal(out, e)
	}
}

func TestDelegateUseFlag(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "")
	var got delegate.Options
	testDeps(t).RunDelegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		got = r.Options
		return delegate.Outcome{Line: "done=true"}, nil
	}
	out, e := executeBusCommand(t, "delegate", "debug_hard", "--task", "task.md", "--use", "codex/sol/high", "--features", "output=diagnosis", "--class", "debug")
	if e != nil || out != "done=true\n" {
		t.Fatal(out, e)
	}
	if got.Entry == nil || *got.Entry != (config.Target{Agent: "codex", Model: "sol", Effort: "high"}) || !got.Person || got.Features["output"] != "diagnosis" || got.Class != "debug" {
		t.Fatalf("%+v", got)
	}
	got = delegate.Options{}
	out, e = executeBusCommand(t, "delegate", "debug_hard", "--task", "task.md", "--use", "sol/high")
	if e == nil || got.Entry != nil || strings.Count(out, "\n") != 1 || !strings.Contains(out, "agent/model/effort") {
		t.Fatal(out, e)
	}
}

func TestDelegateEscalationFlags(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "")
	var got delegate.Options
	ran := 0
	testDeps(t).RunDelegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		got = r.Options
		ran++
		return delegate.Outcome{Line: "done=true"}, nil
	}
	out, e := executeBusCommand(t, "delegate", "debug_hard", "--task", "task.md", "--escalate-from", "task-1", "--reason", "not_green")
	if e != nil || out != "done=true\n" || got.EscalateFrom != "task-1" || got.EscalateReason != "not_green" {
		t.Fatal(out, e, got)
	}
	// One flag without the other is a mistake, not a plain delegation.
	for _, args := range [][]string{{"--escalate-from", "task-1"}, {"--reason", "not_green"}} {
		out, e = executeBusCommand(t, append([]string{"delegate", "debug_hard", "--task", "task.md"}, args...)...)
		if e == nil || ran != 1 || strings.Count(out, "\n") != 1 || !strings.Contains(out, "--escalate-from and --reason go together") {
			t.Fatal(args, out, e)
		}
	}
}

// resultFileRun runs `duolog delegate implement --task task.md --result-file
// <record>` over a running record, with RunDelegate returning out and runErr.
// It returns what the command printed, the running record, the record after
// the run and the command's error.
func resultFileRun(t *testing.T, out delegate.Outcome, runErr error, extra ...string) (string, delegate.Record, delegate.Record, error) {
	t.Helper()
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	testDeps(t).RunDelegate = func(context.Context, delegate.Request) (delegate.Outcome, error) { return out, runErr }
	path := filepath.Join(t.TempDir(), "d-abcdef.json")
	base := delegate.Record{ID: "d-abcdef", Role: "implement", Lead: "lead-a", TaskPath: "task.md", WorkRoot: "/work", State: "running", Started: time.Unix(1, 0).UTC()}
	if err := delegate.WriteRecord(path, base); err != nil {
		t.Fatal(err)
	}
	printed, err := executeBusCommand(t, append([]string{"delegate", "implement", "--task", "task.md", "--result-file", path}, extra...)...)
	got, readErr := delegate.ReadRecord(path, nil)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return printed, base, got, err
}

// sameBase reports whether got keeps the fields of the running record, has
// the PID of the run (this process) and an end time.
func sameBase(got, base delegate.Record) bool {
	return got.ID == base.ID && got.Role == base.Role && got.Lead == base.Lead && got.TaskPath == base.TaskPath && got.WorkRoot == base.WorkRoot && got.PID == os.Getpid() && got.Started.Equal(base.Started) && !got.Ended.IsZero()
}

func TestDelegateResultFileRecordsDone(t *testing.T) {
	printed, base, got, err := resultFileRun(t, delegate.Outcome{Line: "done=true summary=Fixed."}, nil)
	if err != nil || printed != "done=true summary=Fixed.\n" {
		t.Fatal(printed, err)
	}
	if got.State != "done" || got.Line != "done=true summary=Fixed." || !sameBase(got, base) {
		t.Fatalf("%+v", got)
	}
	// The flag is for the server only.
	if help, err := executeBusCommand(t, "delegate", "--help"); err != nil || strings.Contains(help, "result-file") {
		t.Fatal(help, err)
	}
}

func TestDelegateResultFileRecordsRunHere(t *testing.T) {
	p := delegate.Placement{Where: "subagent", Model: "sonnet", Effort: "high", Task: "task.md"}
	printed, base, got, err := resultFileRun(t, delegate.Outcome{RunHere: &p, Line: p.String()}, nil)
	if err != nil || printed != p.String()+"\n" {
		t.Fatal(printed, err)
	}
	if got.State != "run_here" || got.RunHere == nil || *got.RunHere != p || got.Line != p.String() || !sameBase(got, base) {
		t.Fatalf("%+v", got)
	}
}

func TestDelegateResultFileRecordsNeeds(t *testing.T) {
	printed, base, got, err := resultFileRun(t, delegate.Outcome{Needs: []string{"mcp:figma"}, Line: "needs: mcp:figma"}, nil)
	var needs *needsExit
	if !errors.As(err, &needs) || printed != "needs: mcp:figma\n" {
		t.Fatal(printed, err)
	}
	if got.State != "needs" || !slices.Equal(got.Needs, []string{"mcp:figma"}) || !sameBase(got, base) {
		t.Fatalf("%+v", got)
	}
}

func TestDelegateResultFileRecordsFailure(t *testing.T) {
	printed, base, got, err := resultFileRun(t, delegate.Outcome{}, errors.New("worker crashed; see the log"))
	if err == nil || err.Error() != "worker crashed; see the log" || printed != "" {
		t.Fatal(printed, err)
	}
	if got.State != "failed" || got.Line != "worker crashed; see the log" || !sameBase(got, base) {
		t.Fatalf("%+v", got)
	}
	// A refusal before the run, here of the flags, fails the record too, so
	// that it never reads as running or lost.
	printed, _, got, err = resultFileRun(t, delegate.Outcome{Line: "done=true"}, nil, "--use", "sol/high")
	if err == nil || strings.Count(printed, "\n") != 1 || got.State != "failed" || !strings.Contains(got.Line, "agent/model/effort") {
		t.Fatal(printed, err, got)
	}
}

// The detached run puts its own PID in the running record when it starts,
// before the delegation runs, so the server never writes the record after
// starting it.
func TestDelegateResultFileWritesItsPIDAtStart(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	path := filepath.Join(t.TempDir(), "d-abcdef.json")
	if err := delegate.WriteRecord(path, delegate.Record{ID: "d-abcdef", Role: "implement", State: "running", Started: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	var during delegate.Record
	testDeps(t).RunDelegate = func(context.Context, delegate.Request) (delegate.Outcome, error) {
		during, _ = delegate.ReadRecord(path, nil)
		return delegate.Outcome{Line: "done=true"}, nil
	}
	if _, err := executeBusCommand(t, "delegate", "implement", "--task", "task.md", "--result-file", path); err != nil {
		t.Fatal(err)
	}
	if during.State != "running" || during.PID != os.Getpid() {
		t.Fatalf("record during the run: %+v", during)
	}
	got, err := delegate.ReadRecord(path, nil)
	if err != nil || got.State != "done" || got.PID != os.Getpid() {
		t.Fatalf("%+v %v", got, err)
	}
}

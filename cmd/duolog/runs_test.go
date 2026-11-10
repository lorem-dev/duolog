package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/policy"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	corerun "github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const runLogText = `{"type":"assistant","message":{"content":[{"type":"text","text":"Reading now."}]}}` + "\n" +
	`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}]}}` + "\n" +
	`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"From Codex."}}` + "\n" +
	`{"type":"mystery"}` + "\n"

// runsProject is a person's project with the records of the given runs. A
// record is written as given, with its log under the work root.
func runsProject(t *testing.T, records ...delegate.Record) (root string) {
	t.Helper()
	root, _ = telemetryCommandSetup(t)
	asTerminal(t, true)
	withAgentAncestor(t, "")
	t.Setenv("DUOLOG_DEPTH", "")
	for _, r := range records {
		if r.WorkRoot == "" {
			r.WorkRoot = filepath.Join(root, ".worktrees", "feature")
		}
		if r.Log != "" {
			log := filepath.Join(r.WorkRoot, filepath.FromSlash(r.Log))
			if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(log, []byte(runLogText), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := delegate.WriteRecord(filepath.Join(delegate.RecordDir(filepath.Join(root, ".duolog")), r.ID+".json"), r); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runRec(id, state string, age time.Duration) delegate.Record {
	r := delegate.Record{ID: id, Role: "implement", Agent: "claude", Model: "sonnet", State: state, Started: time.Now().UTC().Add(-age), Log: ".duolog/tasks/task-" + id + "/stdout.log"}
	if state == "running" {
		r.PID = os.Getpid()
	} else {
		r.Ended = r.Started.Add(time.Minute)
	}
	return r
}

func TestRunsListsRunningFirstThenTheLastFinished(t *testing.T) {
	var records []delegate.Record
	for i := 0; i < 8; i++ {
		records = append(records, runRec(fmt.Sprintf("d-done%02d", i), "done", time.Duration(i+10)*time.Minute))
	}
	records = append(records, runRec("d-oldrun", "running", 3*time.Hour), runRec("d-newrun", "running", 2*time.Minute))
	runsProject(t, records...)

	out, err := executeBusCommand(t, "runs")
	if err != nil {
		t.Fatal(err)
	}
	want := "d-newrun running\nd-oldrun running\nd-done00 done\nd-done01 done\nd-done02 done\nd-done03 done\nd-done04 done\n"
	if out != want {
		t.Fatalf("runs =\n%s\nwant\n%s", out, want)
	}

	out, err = executeBusCommand(t, "runs", "--long")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 8 {
		t.Fatalf("long =\n%s", out)
	}
	for i, want := range []string{"ID", "KIND", "ROLE", "AGENT", "STATE", "AGE", "WORKTREE"} {
		if f := strings.Fields(lines[0]); i >= len(f) || f[i] != want {
			t.Fatalf("header = %q", lines[0])
		}
	}
	if f := strings.Fields(lines[1]); strings.Join(f, " ") != "d-newrun delegate implement claude/sonnet running 2m feature" {
		t.Fatalf("row = %q", lines[1])
	}
}

type jsonRunRow struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Role     string  `json:"role"`
	Agent    string  `json:"agent"`
	Model    string  `json:"model"`
	State    string  `json:"state"`
	Started  *string `json:"started"`
	Worktree string  `json:"worktree"`
	TaskDir  string  `json:"task_dir"`
}

func TestRunsJSONHasOneShapeForPeersAndDelegateRuns(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	asTerminal(t, true)
	withAgentAncestor(t, "")
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-list", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:old", Task: ".duolog/task.md", WorkRoot: root}); err != nil {
		t.Fatal(err)
	}
	rec := runRec("d-newrun", "running", 2*time.Minute)
	rec.WorkRoot = filepath.Join(root, ".worktrees", "feature")
	if err := delegate.WriteRecord(filepath.Join(delegate.RecordDir(filepath.Join(root, ".duolog")), rec.ID+".json"), rec); err != nil {
		t.Fatal(err)
	}

	out, err := executeBusCommand(t, "runs", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []jsonRunRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("%s %v", out, err)
	}
	p, r := rows[0], rows[1]
	if p.ID != "peer-list" || p.Kind != "peer" || p.Role != "plan" || p.Agent != "claude" || !oldPeerStates[p.State] || p.Started != nil || p.TaskDir != filepath.Join(root, ".duolog") {
		t.Fatalf("peer row = %+v", p)
	}
	want := jsonRunRow{ID: "d-newrun", Kind: "delegate", Role: "implement", Agent: "claude", Model: "sonnet", State: "running", Worktree: rec.WorkRoot, TaskDir: filepath.Join(rec.WorkRoot, ".duolog", "tasks", "task-d-newrun")}
	if r.Started == nil {
		t.Fatalf("delegate row has no start: %+v", r)
	}
	r.Started = nil
	if r != want {
		t.Fatalf("delegate row = %+v, want %+v", r, want)
	}
	for _, key := range []string{"id", "kind", "role", "agent", "model", "state", "started", "worktree", "task_dir"} {
		if !strings.Contains(out, `"`+key+`":`) {
			t.Fatalf("no key %s: %s", key, out)
		}
	}

	out, err = executeBusCommand(t, "runs", "--long")
	if err != nil || !strings.Contains(out, "peer-list") || !strings.Contains(out, "peer") {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := executeBusCommand(t, "runs", "--long", "--json"); err == nil {
		t.Fatal("--long and --json were accepted together")
	}
}

func TestRunsJSONWithNothingIsAnEmptyList(t *testing.T) {
	runsProject(t)
	out, err := executeBusCommand(t, "runs", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestWatchPeerShowsTheScreenAndRefusesModels(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordShowPeer(t, root)
	clearModelMarkers(t)
	asTerminal(t, true)
	withAgentAncestor(t, "")
	out, err := executeBusCommand(t, "watch", "peer-list")
	if err != nil || !strings.Contains(out, "peer-list claude-plan") {
		t.Fatalf("%q %v", out, err)
	}
	asTerminal(t, false)
	out, err = executeBusCommand(t, "watch", "peer-list")
	if err == nil || !strings.Contains(err.Error(), "only to your own terminal") || strings.Contains(out, "peer-list claude-plan") {
		t.Fatalf("%q %v", out, err)
	}
	asTerminal(t, true)
	if _, err := executeBusCommand(t, "watch", "peer-nothere"); err == nil || !strings.Contains(err.Error(), "peer-nothere") {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestRunsWithNoRunsPrintsNothingElse(t *testing.T) {
	runsProject(t)
	for _, args := range [][]string{{"runs"}, {"runs", "--long"}} {
		out, err := executeBusCommand(t, args...)
		if err != nil || strings.Contains(out, "d-") {
			t.Fatalf("%v: %q %v", args, out, err)
		}
	}
}

func TestRunsRefusedForWorker(t *testing.T) {
	runsProject(t, runRec("d-aaaaaa", "done", time.Minute))
	t.Setenv("DUOLOG_DEPTH", "1")
	if out, err := executeBusCommand(t, "runs"); err == nil {
		t.Fatalf("a worker listed runs: %q", out)
	}
}

func TestWatchEndsWithTheRunAndPrintsMessagesAndToolLines(t *testing.T) {
	finished := runRec("d-oldrun", "done", time.Hour)
	finished.Line = "done=true"
	runsProject(t, finished)
	out, err := executeBusCommand(t, "watch", "d-oldrun")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"d-oldrun implement claude/sonnet", "Reading now.", "> Read a.go", "From Codex.", "ended: done"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "mystery") || strings.Contains(out, `"type"`) {
		t.Fatalf("raw JSON in output:\n%s", out)
	}
}

func TestWatchWithoutAnIdNeedsARunningRun(t *testing.T) {
	runsProject(t, runRec("d-oldrun", "done", time.Hour))
	if out, err := executeBusCommand(t, "watch"); err == nil || !strings.Contains(err.Error(), "duolog runs") {
		t.Fatalf("%q %v", out, err)
	}
	if out, err := executeBusCommand(t, "watch", "d-nothere"); err == nil || !strings.Contains(err.Error(), "d-nothere") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestWatchWithoutAnIdFollowsTheNewestRunningRun(t *testing.T) {
	older := runRec("d-midrun", "running", 5*time.Minute)
	newer := runRec("d-newrun", "running", time.Minute)
	root := runsProject(t, older, newer)
	record := filepath.Join(delegate.RecordDir(filepath.Join(root, ".duolog")), "d-newrun.json")
	go func() {
		time.Sleep(50 * time.Millisecond)
		newer.State, newer.Ended, newer.PID = "done", time.Now().UTC(), 0
		// On Windows store.Rename gives up after eight tries while watch
		// keeps the record open; a lost write leaves watch waiting until
		// the test times out.
		for i := 0; i < 200 && delegate.WriteRecord(record, newer) != nil; i++ {
			time.Sleep(10 * time.Millisecond)
		}
	}()
	out, err := executeBusCommand(t, "watch")
	if err != nil || !strings.HasPrefix(out, "d-newrun ") || !strings.Contains(out, "ended: done") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestWatchRefusedForModels(t *testing.T) {
	runsProject(t, runRec("d-aaaaaa", "done", time.Minute))
	const refusal = "only to your own terminal"
	check := func(label string) {
		t.Helper()
		out, err := executeBusCommand(t, "watch", "d-aaaaaa")
		if err == nil || !strings.Contains(err.Error(), refusal) || strings.Contains(out, "Reading now.") {
			t.Fatalf("%s: %q %v", label, out, err)
		}
	}
	for _, name := range modelMarkers {
		clearModelMarkers(t)
		t.Setenv(name, "1")
		asTerminal(t, true)
		check(name)
	}
	clearModelMarkers(t)
	asTerminal(t, false)
	check("not a terminal")
	asTerminal(t, true)
	t.Setenv("DUOLOG_DEPTH", "1")
	check("worker")
	t.Setenv("DUOLOG_DEPTH", "")
	for _, agent := range []string{"claude", "codex"} {
		withAgentAncestor(t, agent)
		out, err := executeBusCommand(t, "watch", "d-aaaaaa")
		if err == nil || !strings.Contains(err.Error(), agent) || strings.Contains(out, "Reading now.") {
			t.Fatalf("%s: %q %v", agent, out, err)
		}
	}
}

func watchRex(t *testing.T) string {
	t.Helper()
	return testbin.Install(t, "rex", testbin.Scenario{Exit: 99, Stderr: "unexpected rex command", Responses: map[string]testbin.Scenario{
		"new":           {Stdout: peerSplitReply},
		"window new":    {Stdout: peerSplitReply},
		"block inspect": {Stdout: `{"block":{"window_id":"window:lead"}}`},
		"wait":          {Exit: 1, Stderr: "Error: wait timed out after 500ms: context deadline exceeded"},
		"client ls":     {Stdout: viewClientReply},
		"-C":            {Stdout: "{}"},
		"block ls":      {Stdout: `{"blocks":[{"id":"block:peer"}]}`},
	}})
}

func TestWatchTabOpensARexTabAndPrintsOneLine(t *testing.T) {
	root := runsProject(t, runRec("d-aaaaaa", "running", time.Minute))
	t.Setenv("REX_SESSION", "sess")
	t.Setenv("REX_BIN_DIR", "")
	t.Setenv("DUOLOG_LEAD", "")
	rex := watchRex(t)
	_ = root

	// A lead's call may use --tab: the output is one line, and no run text.
	asTerminal(t, false)
	out, err := executeBusCommand(t, "watch", "--tab", "d-aaaaaa")
	if err != nil || strings.Count(out, "\n") != 1 || strings.Contains(out, "Reading now.") {
		t.Fatalf("%q %v", out, err)
	}
	if !strings.Contains(out, "d-aaaaaa shown on client client:app: duolog | watch d-aaaaaa") {
		t.Fatalf("line = %q", out)
	}
	var opened bool
	for _, c := range rexCalls(t, rex) {
		joined := strings.Join(c, " ")
		if c[0] == "send" || c[0] == "send-key" || c[0] == "capture" {
			t.Fatalf("the tab was typed into or read: %v", c)
		}
		if strings.Contains(joined, " watch d-aaaaaa") {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("no rex call started duolog watch d-aaaaaa: %v", rexCalls(t, rex))
	}
}

func TestWatchTabWithoutRexPrintsTheCommand(t *testing.T) {
	runsProject(t, runRec("d-aaaaaa", "running", time.Minute))
	t.Setenv("REX_SESSION", "")
	out, err := executeBusCommand(t, "watch", "--tab", "d-aaaaaa")
	if err != nil || strings.TrimSpace(out) != "no Rex here; run in your own terminal: duolog watch d-aaaaaa" {
		t.Fatalf("%q %v", out, err)
	}
	// With no id the newest running run is named.
	out, err = executeBusCommand(t, "watch", "--tab")
	if err != nil || !strings.Contains(out, "duolog watch d-aaaaaa") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestWatchTabRefusedForWorker(t *testing.T) {
	runsProject(t, runRec("d-aaaaaa", "running", time.Minute))
	t.Setenv("REX_SESSION", "sess")
	rex := watchRex(t)
	t.Setenv("DUOLOG_DEPTH", "1")
	if _, err := executeBusCommand(t, "watch", "--tab", "d-aaaaaa"); err == nil {
		t.Fatal("a worker opened a watch tab")
	}
	if _, err := os.Stat(rex + ".calls.ndjson"); !os.IsNotExist(err) {
		t.Fatalf("rex was called: %v", rexCalls(t, rex))
	}
}

// A lead's tool call that names duolog watch, behind any wrapper, is asked
// (Claude) or denied (Codex), as peer show is. duolog runs and duolog watch
// --tab pass: they put no worker output in the lead's context.
func TestWatchLeadGuard(t *testing.T) {
	root, _ := telemetryCommandSetup(t)
	stateDir := filepath.Join(root, ".duolog")
	guarded := []string{
		"duolog watch", "duolog watch d-abcdef", "duolog watch d-abcdef --lead L",
		"env -u CLAUDECODE script -q /dev/null duolog watch d-abcdef",
		`sh -c "duolog watch d-abcdef"`,
		"rex split -s sess --shell=none -- duolog watch d-abcdef",
		"duolog --lead L watch d-abcdef",
		"true&&scripts/dev/bin/duolog watch",
		"duolog runs; duolog.exe watch d-abcdef",
		"echo $(duolog watch d-abcdef)",
		"duolog watch d-abcdef; echo --tab",
		"duolog watch --tab=false d-abcdef",
		"duolog watch d-abcdef && duolog watch --tab d-abcdef",
		"duolog watch d-abcdef # --tab",
		"env -u CLAUDECODE duolog watch d-abcdef",
		"FOO=1 duolog watch d-abcdef", "A= B= script -q /dev/null duolog watch d-abcdef",
		"duolog watch --lead --tab d-abcdef", "duolog watch d-abcdef --client --tab",
		`sh -c "echo hi; duolog watch d-abcdef"`, "~/bin/duolog watch d-abcdef",
	}
	open := []string{
		"duolog runs", "duolog runs --long", "duolog watch --tab", "duolog watch --tab d-abcdef",
		"duolog watch d-abcdef --tab", "duolog --lead L watch --tab d-abcdef",
		"duolog peer view peer-x", "go test ./cmd/duolog", "echo watch the build",
		`rg "duolog watch" docs`, `git commit -m "add duolog watch"`, "rg -n duolog watch docs",
		"FOO=1 duolog watch --tab d-abcdef", `rg "x | duolog watch" docs`, `rg "x; duolog watch" docs`,
		"git commit -m \"fix\nduolog watch d-abcdef\"", `git commit -m "a && duolog watch d-abcdef"`,
	}
	for _, agent := range []string{"claude", "codex"} {
		for _, command := range guarded {
			d, ok := leadGuard(stateDir, agent, leadToolInput(agent, root, "Bash", map[string]string{"command": command}))
			want := map[string]string{"claude": policy.Ask, "codex": policy.Deny}[agent]
			if !ok || d.Kind != want || !strings.Contains(d.Reason, "duolog watch") {
				t.Errorf("%s %q: %+v %v", agent, command, d, ok)
			}
		}
		for _, command := range open {
			if d, ok := leadGuard(stateDir, agent, leadToolInput(agent, root, "Bash", map[string]string{"command": command})); ok {
				t.Errorf("%s %q guarded: %+v", agent, command, d)
			}
		}
	}
}

func TestWatchTabForAPeerDoesWhatPeerViewDoes(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{})
	clearModelMarkers(t)
	asTerminal(t, false)
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-list", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:peers"}); err != nil {
		t.Fatal(err)
	}
	rex := viewRex(t)
	out, err := executeBusCommand(t, "watch", "--tab", "peer-list")
	if want := "peer-list shown on client client:app: duolog | claude-plan; to return, use Previous Session in Rex"; err != nil || strings.TrimSpace(out) != want {
		t.Fatalf("%q %v", out, err)
	}
	for _, c := range rexCalls(t, rex) {
		if c[0] == "capture" || c[0] == "send" || c[0] == "send-key" {
			t.Fatalf("watch --tab touched the pane: %v", c)
		}
	}
}

// fakeWorker is a delegate.Worker that counts its runs.
type fakeWorker struct{ ran int }

func (w *fakeWorker) Run(context.Context, corerun.Spec) (corerun.Result, error) {
	w.ran++
	return corerun.Result{Done: true}, nil
}

func (*fakeWorker) Launch(corerun.Spec) []string { return nil }

// The detached run puts its worker, model and log in its record when the
// worker starts, without changing the worker's run.
func TestRecordingWorkerWritesAgentModelAndLog(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(delegate.RecordDir(t.TempDir()), "d-abcdef.json")
	if err := delegate.WriteRecord(path, delegate.Record{ID: "d-abcdef", WorkRoot: work, PID: 7, State: "running", Started: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	inner := &fakeWorker{}
	deps := delegate.Deps{Agents: map[string]delegate.Agent{"claude": {Worker: inner}}}
	wrapped := recordingDeps(deps, path)
	spec := corerun.Spec{Route: coreroute.Route{Target: config.Target{Agent: "claude", Model: "sonnet-x"}}, TaskDir: filepath.Join(work, ".duolog", "tasks", "task-1")}
	res, err := wrapped.Agents["claude"].Worker.Run(context.Background(), spec)
	if err != nil || !res.Done || inner.ran != 1 {
		t.Fatalf("%+v %v ran=%d", res, err, inner.ran)
	}
	got, err := delegate.ReadRecord(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != "claude" || got.Model != "sonnet-x" || got.Log != ".duolog/tasks/task-1/stdout.log" || got.PID != 7 || got.State != "running" {
		t.Fatalf("record = %+v", got)
	}
	if deps.Agents["claude"].Worker != delegate.Worker(inner) {
		t.Fatal("the caller's deps were changed")
	}
	// A record that has ended is left as it is.
	if err := delegate.WriteRecord(path, delegate.Record{ID: "d-abcdef", WorkRoot: work, State: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped.Agents["claude"].Worker.Run(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if got, _ = delegate.ReadRecord(path, nil); got.Agent != "" || got.State != "done" {
		t.Fatalf("ended record = %+v", got)
	}
}

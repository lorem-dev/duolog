package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func mcpPeerCall(t *testing.T, in mcp.PeerInput) (mcp.PeerOutput, error) {
	t.Helper()
	return testDeps(t).mcpPeer(context.Background(), in)
}

func mustMCPPeer(t *testing.T, in mcp.PeerInput) mcp.PeerOutput {
	t.Helper()
	out, err := mcpPeerCall(t, in)
	if err != nil {
		t.Fatalf("%+v: %v", in, err)
	}
	return out
}

// mcpTaskFixture is a task whose read and write file exists in root.
func mcpTaskFixture(t *testing.T, root string) *mcp.Task {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &mcp.Task{Goal: "Fix a.", Read: []string{"a.go"}, Write: []string{"a.go"}, Limits: []string{"small"}, Check: []string{"go vet ./..."}}
}

// mcpSentPeer opens a peer through the tool and sends it a task, so that a
// wait has a task to wait for. It returns the project root and the peer id.
func mcpSentPeer(t *testing.T) (root, id string) {
	t.Helper()
	root, _ = peerProject(t, sequence(claudeIdleScreen()))
	id = mustMCPPeer(t, mcp.PeerInput{Action: "open", Role: "plan"}).ID
	line := "Read .duolog/tasks/" + id + "/task.md and do it. Do not call rex. Write the answer to " + policy.ReportPath(id) + ". Reply \"done\"."
	peerRex(t, claudeIdleScreen(), claudeIdleScreen(), claudeHolding(line), claudeAtWork())
	mustMCPPeer(t, mcp.PeerInput{Action: "send", ID: id, Task: mcpTaskFixture(t, root)})
	return root, id
}

func TestMCPPeerOpenReturnsEntry(t *testing.T) {
	peerProject(t, sequence(claudeIdleScreen()))
	out := mustMCPPeer(t, mcp.PeerInput{Action: "open", Role: "plan"})
	if !strings.HasPrefix(out.ID, "peer-") || out.State != "ready" || out.Line != out.ID+" ready" || out.RunHere != nil {
		t.Fatalf("%+v", out)
	}
}

func TestMCPPeerSendWritesTaskFile(t *testing.T) {
	root, _ := peerProject(t, sequence(claudeIdleScreen()))
	id := mustMCPPeer(t, mcp.PeerInput{Action: "open", Role: "plan"}).ID
	rel := ".duolog/tasks/" + id + "/task.md"
	line := "Read " + rel + " and do it. Do not call rex. Write the answer to " + policy.ReportPath(id) + ". Reply \"done\"."
	peerRex(t, claudeIdleScreen(), claudeIdleScreen(), claudeHolding(line), claudeAtWork())
	rex, err := exec.LookPath("rex")
	if err != nil {
		t.Fatal(err)
	}
	task := mcpTaskFixture(t, root)
	out := mustMCPPeer(t, mcp.PeerInput{Action: "send", ID: id, Task: task})
	if out.ID != id || out.TaskPath != rel || out.Line != id+" sent "+rel {
		t.Fatalf("%+v", out)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := taskspec.Render(taskspec.Spec(*task))
	if err != nil || string(got) != string(want) {
		t.Fatalf("file = %q, want %q (%v)", got, want, err)
	}
	sent := false
	for _, c := range rexCalls(t, rex) {
		if c[0] == "send" && strings.Contains(strings.Join(c, " "), line) {
			sent = true
		}
	}
	if !sent {
		t.Fatalf("the pane did not get %q: %v", line, rexCalls(t, rex))
	}
	if _, err := mcpPeerCall(t, mcp.PeerInput{Action: "send", ID: id}); err == nil || !strings.Contains(err.Error(), "task") {
		t.Fatalf("a send without a task: %v", err)
	}
}

func TestMCPPeerSendRunHereIsField(t *testing.T) {
	root, _ := peerProject(t, sequence(claudeIdleScreen()))
	id := mustMCPPeer(t, mcp.PeerInput{Action: "open", Role: "plan"}).ID
	task := mcpTaskFixture(t, root)
	testbin.Install(t, "rex", testbin.Scenario{Exit: 99, Responses: map[string]testbin.Scenario{
		"block inspect": {Stdout: `{"block":{"window_id":"window:lead"}}`},
		"wait":          {Exit: 1, Stderr: "Error: wait timed out after 500ms: context deadline exceeded"},
		"capture":       {Stdout: "You've hit your 5-hour limit · resets 3am (Europe/London)\n" + claudeIdleScreen()},
	}})
	out := mustMCPPeer(t, mcp.PeerInput{Action: "send", ID: id, Task: task})
	if !strings.HasPrefix(out.Line, "run here: ") || out.RunHere == nil || out.RunHere.Task == "" {
		t.Fatalf("%+v", out)
	}
	if out.RunHere.Mode != "self" && out.RunHere.Mode != "subagent" {
		t.Fatalf("mode %q", out.RunHere.Mode)
	}
}

func TestMCPPeerWaitReturnsReport(t *testing.T) {
	root, id := mcpSentPeer(t)
	var lines []string
	for i := 1; i <= 60; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	report := filepath.Join(root, filepath.FromSlash(policy.ReportPath(id)))
	if err := os.WriteFile(report, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	peerRex(t, claudeAtWork(), claudeIdleScreen(), claudeIdleScreen())
	out := mustMCPPeer(t, mcp.PeerInput{Action: "wait", ID: id})
	if out.ReportPath != policy.ReportPath(id) || out.Report != strings.Join(lines[:40], "\n")+"\n" {
		t.Fatalf("%+v", out)
	}
	if !strings.Contains(out.Line, "report "+policy.ReportPath(id)) {
		t.Fatalf("line %q", out.Line)
	}
}

func TestMCPPeerReportHeadKeeps4KB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 5000)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := reportHead(path); err != nil || len(got) != mcpReportBytes {
		t.Fatalf("%d bytes, %v", len(got), err)
	}
}

func TestMCPPeerWaitTimesOutWithState(t *testing.T) {
	_, id := mcpSentPeer(t)
	unit := mcpPeerWaitUnit
	mcpPeerWaitUnit = 20 * time.Millisecond
	t.Cleanup(func() { mcpPeerWaitUnit = unit })
	peerRex(t, claudeAtWork())
	out := mustMCPPeer(t, mcp.PeerInput{Action: "wait", ID: id, TimeoutMinutes: 2})
	if out.ID != id || out.State == "" || !strings.Contains(out.Line, "still "+out.State+" after waiting") {
		t.Fatalf("%+v", out)
	}
}

func TestMCPPeerWaitCapsTimeout(t *testing.T) {
	for in, want := range map[int]time.Duration{0: 10 * time.Minute, 3: 3 * time.Minute, 60: 10 * time.Minute, -5: 10 * time.Minute} {
		if got := mcpPeerTimeout(in); got != want {
			t.Fatalf("timeout %d: %v, want %v", in, got, want)
		}
	}
}

func TestMCPPeerWaitListsOpenAsks(t *testing.T) {
	root, id := mcpSentPeer(t)
	b, err := bus.Open(filepath.Join(root, ".duolog"), config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	own, err := b.Send(bus.Message{From: "codex/ask.test", To: "codex/lead.lead-1", K: policy.Ask, S: "exec custom", W: 120})
	if err != nil {
		t.Fatal(err)
	}
	other := session.Address(session.Lead{ID: "lead-b", Target: config.Target{Agent: "claude"}})
	if _, err := b.Send(bus.Message{From: "codex/ask.other", To: other, K: policy.Ask, S: "exec other", W: 120}); err != nil {
		t.Fatal(err)
	}
	peerRex(t, claudeAtWork(), claudeIdleScreen(), claudeIdleScreen())
	out := mustMCPPeer(t, mcp.PeerInput{Action: "wait", ID: id})
	want := []mcp.Ask{{N: own, From: "codex/ask.test", Summary: "exec custom"}}
	if fmt.Sprint(out.OpenAsks) != fmt.Sprint(want) {
		t.Fatalf("asks %+v, want %+v", out.OpenAsks, want)
	}
}

func TestMCPPeerAnswerThenStatus(t *testing.T) {
	switchModel := startupFixture(t, "claude-model-switch-question.txt")
	peerProject(t, sequence(switchModel, switchModel, switchModel, claudeIdleScreen(), claudeIdleScreen()))
	opened := mustMCPPeer(t, mcp.PeerInput{Action: "open", Role: "plan"})
	if opened.State != "question" || !strings.HasPrefix(opened.Question, "Switch model?") {
		t.Fatalf("open: %+v", opened)
	}
	if st := mustMCPPeer(t, mcp.PeerInput{Action: "status", ID: opened.ID}); st.State != "question" || !strings.Contains(st.Line, "question") {
		t.Fatalf("status: %+v", st)
	}
	answered := mustMCPPeer(t, mcp.PeerInput{Action: "answer", ID: opened.ID, Option: "Yes"})
	if answered.Line != opened.ID+" ready" || answered.State != "ready" {
		t.Fatalf("answer: %+v", answered)
	}
	if st := mustMCPPeer(t, mcp.PeerInput{Action: "status", ID: opened.ID}); st.State != "ready" || st.Line != opened.ID+" ready" {
		t.Fatalf("status: %+v", st)
	}
}

func TestMCPPeerCloseLineMatchesCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live peer sessions are unavailable on Windows")
	}
	gone := func(t *testing.T) {
		t.Helper()
		clearModelMarkers(t)
		asTerminal(t, true)
		root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
		recordListPeer(t, root)
		restartRex(t, root)
	}
	var cli, tool string
	t.Run("cli", func(t *testing.T) {
		gone(t)
		cli = peerLine(t, "peer", "close", "peer-list")
	})
	t.Run("tool", func(t *testing.T) {
		gone(t)
		tool = mustMCPPeer(t, mcp.PeerInput{Action: "close", ID: "peer-list"}).Line
	})
	if cli == "" || tool != cli || !strings.Contains(tool, "its pane was already gone") {
		t.Fatalf("tool %q, cli %q", tool, cli)
	}
}

func TestMCPPeerListRows(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	out := mustMCPPeer(t, mcp.PeerInput{Action: "list"})
	want := []mcp.PeerRow{{ID: "peer-list", Role: "plan", Agent: "claude", State: "ready", Task: ".duolog/task.md"}}
	if fmt.Sprint(out.Peers) != fmt.Sprint(want) {
		t.Fatalf("%+v", out.Peers)
	}
}

func TestMCPPeerUnknownAction(t *testing.T) {
	_, rex := peerProject(t, testbin.Scenario{})
	for _, in := range []mcp.PeerInput{
		{Action: "nap"},
		{},
		{Action: "open"},
		{Action: "wait"},
		{Action: "answer", ID: "peer-x"},
	} {
		if _, err := mcpPeerCall(t, in); err == nil {
			t.Fatalf("%+v was accepted", in)
		}
	}
	_, err := mcpPeerCall(t, mcp.PeerInput{Action: "nap"})
	if err == nil || !strings.Contains(err.Error(), "unknown peer action") || !strings.Contains(err.Error(), "open, send, wait") {
		t.Fatalf("%v", err)
	}
	if len(rexCalls(t, rex)) != 0 {
		t.Fatal("a refused call reached rex")
	}
}

func TestMCPPeerRefusesUnderWorker(t *testing.T) {
	_, rex := peerProject(t, testbin.Scenario{})
	t.Setenv("DUOLOG_DEPTH", "1")
	for _, in := range []mcp.PeerInput{
		{Action: "open", Role: "plan"},
		{Action: "wait", ID: "peer-x"},
		{Action: "status", ID: "peer-x"},
		{Action: "close", ID: "peer-x"},
		{Action: "list"},
	} {
		if _, err := mcpPeerCall(t, in); err == nil || !strings.Contains(err.Error(), "workers cannot open or control peer sessions") {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	if len(rexCalls(t, rex)) != 0 {
		t.Fatal("a worker reached rex")
	}
}

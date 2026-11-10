package main

import (
	"context"
	"encoding/json"
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

func mcpPeerProject(t *testing.T, capture testbin.Scenario) (string, string) {
	t.Helper()
	return peerProject(t, capture)
}

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
	root, _ = mcpPeerProject(t, sequence(claudeIdleScreen()))
	id = mustMCPPeer(t, mcp.PeerInput{Action: "open", Role: "plan"}).ID
	line := "Read .duolog/tasks/" + id + "-send-1/task.md and do it. Do not call rex. Write the answer to " + policy.ReportPath(id) + ". Reply \"done\"."
	peerRex(t, claudeIdleScreen(), claudeIdleScreen(), claudeHolding(line), claudeAtWork())
	mustMCPPeer(t, mcp.PeerInput{Action: "send", ID: id, Task: mcpTaskFixture(t, root)})
	return root, id
}

func TestMCPPeerOpenReturnsEntry(t *testing.T) {
	mcpPeerProject(t, sequence(claudeIdleScreen()))
	out := mustMCPPeer(t, mcp.PeerInput{Action: "open", Role: "plan"})
	if !strings.HasPrefix(out.ID, "peer-") || out.State != "ready" || out.Line != out.ID+" ready" || out.RunHere != nil {
		t.Fatalf("%+v", out)
	}
}

// mcpLinkedWorktree gives the temporary project a linked checkout, without
// touching the repository that runs these tests.
func mcpLinkedWorktree(t *testing.T, root, work string) {
	t.Helper()
	git := filepath.Join(root, ".git")
	for _, dir := range []string{"objects", "refs/heads", "worktrees/feature"} {
		if err := os.MkdirAll(filepath.Join(git, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, text := range map[string]string{
		filepath.Join(git, "HEAD"):                        "ref: refs/heads/main\n",
		filepath.Join(git, "config"):                      "[core]\nrepositoryformatversion=0\nbare=false\n",
		filepath.Join(git, "worktrees/feature/HEAD"):      "ref: refs/heads/feature\n",
		filepath.Join(git, "worktrees/feature/commondir"): "../..\n",
		filepath.Join(git, "worktrees/feature/gitdir"):    filepath.Join(work, ".git") + "\n",
		filepath.Join(work, ".git"):                       "gitdir: " + filepath.Join(git, "worktrees/feature") + "\n",
	} {
		putWorktreeFile(t, path, text)
	}
}

func mcpOpenInWorktree(t *testing.T, worktree string) (mcp.PeerOutput, error) {
	t.Helper()
	// Exercise the JSON field a real MCP client sends, including its decoding.
	data, err := json.Marshal(map[string]string{"action": "open", "role": "plan", "worktree": worktree})
	if err != nil {
		t.Fatal(err)
	}
	var in mcp.PeerInput
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	return mcpPeerCall(t, in)
}

func TestMCPPeerOpenInWorktree(t *testing.T) {
	for _, kind := range []string{"name", "relative", "absolute", "external"} {
		t.Run(kind, func(t *testing.T) {
			root, rex := mcpPeerProject(t, sequence(claudeIdleScreen()))
			work := filepath.Join(root, ".worktrees", "feature")
			if kind == "external" {
				work = t.TempDir()
			}
			mcpLinkedWorktree(t, root, work)
			arg := work
			switch kind {
			case "name":
				arg = "feature"
			case "relative":
				arg = ".worktrees/feature"
			}
			out, err := mcpOpenInWorktree(t, arg)
			if err != nil {
				t.Fatal(err)
			}
			reg, err := session.Open(filepath.Join(root, ".duolog"))
			if err != nil {
				t.Fatal(err)
			}
			entries, err := reg.Entries()
			if err != nil || len(entries) != 1 || entries[0].ID != out.ID || entries[0].WorkRoot != work {
				t.Fatalf("entries = %+v, want peer in %s (%v)", entries, work, err)
			}
			opened := false
			for _, call := range rexCalls(t, rex) {
				if call[0] == "new" && strings.Contains(strings.Join(call, " "), work) {
					opened = true
				}
			}
			if !opened {
				t.Fatalf("Rex did not open in %s: %v", work, rexCalls(t, rex))
			}
			if cwd, err := os.Getwd(); err != nil || cwd != root {
				t.Fatalf("server cwd changed: %s (%v)", cwd, err)
			}
		})
	}
}

// A plain name means a worktree first, even when a folder in the working
// folder has the same name.
func TestMCPPeerOpenPrefersWorktreeName(t *testing.T) {
	root, _ := mcpPeerProject(t, sequence(claudeIdleScreen()))
	work := filepath.Join(root, ".worktrees", "docs")
	mcpLinkedWorktree(t, root, work)
	putWorktreeFile(t, filepath.Join(root, "docs", "guide.md"), "text\n")
	out, err := mcpOpenInWorktree(t, "docs")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := reg.Entries()
	if err != nil || len(entries) != 1 || entries[0].ID != out.ID || entries[0].WorkRoot != work {
		t.Fatalf("entries = %+v, want peer in %s (%v)", entries, work, err)
	}
}

// A folder of the project that is not a checkout root is no place for a peer:
// it would edit the wrong tree with no error.
func TestMCPPeerOpenRefusesFolderThatIsNoCheckout(t *testing.T) {
	for _, kind := range []string{"plain name", "subfolder", "git folder", "state folder", "nested repository"} {
		t.Run(kind, func(t *testing.T) {
			root, rex := mcpPeerProject(t, sequence(claudeIdleScreen()))
			mcpLinkedWorktree(t, root, filepath.Join(root, ".worktrees", "feature"))
			putWorktreeFile(t, filepath.Join(root, "docs", "guide.md"), "text\n")
			arg := map[string]string{"plain name": "docs", "subfolder": filepath.Join(root, "docs"), "git folder": filepath.Join(root, ".git"), "state folder": filepath.Join(root, ".duolog"), "nested repository": filepath.Join(root, "vendor", "lib")}[kind]
			if kind == "nested repository" {
				git := filepath.Join(arg, ".git")
				for _, dir := range []string{"objects", "refs/heads"} {
					if err := os.MkdirAll(filepath.Join(git, dir), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				putWorktreeFile(t, filepath.Join(git, "HEAD"), "ref: refs/heads/main\n")
				putWorktreeFile(t, filepath.Join(git, "config"), "[core]\nrepositoryformatversion=0\nbare=false\n")
			}
			if _, err := mcpOpenInWorktree(t, arg); err == nil || !strings.Contains(err.Error(), "not a checkout of this repository") {
				t.Fatalf("%s: %v", arg, err)
			}
			if calls := rexCalls(t, rex); len(calls) != 0 {
				t.Fatalf("refused folder reached Rex: %v", calls)
			}
		})
	}
}

func TestMCPPeerOpenRefusesInvalidWorktree(t *testing.T) {
	for _, kind := range []string{"missing", "file", "unrelated"} {
		t.Run(kind, func(t *testing.T) {
			root, rex := mcpPeerProject(t, sequence(claudeIdleScreen()))
			arg := filepath.Join(root, "missing")
			switch kind {
			case "file":
				putWorktreeFile(t, arg, "not a folder\n")
			case "unrelated":
				arg = t.TempDir()
			}
			if _, err := mcpOpenInWorktree(t, arg); err == nil {
				t.Fatal("invalid worktree accepted")
			}
			if calls := rexCalls(t, rex); len(calls) != 0 {
				t.Fatalf("invalid worktree reached Rex: %v", calls)
			}
		})
	}
}

func TestMCPPeerSendUsesRegisteredWorkRoot(t *testing.T) {
	root, _ := mcpPeerProject(t, sequence(claudeIdleScreen()))
	// Model real account state, shared across the lead's working folders.
	skillGate(t, false)
	work := filepath.Join(root, ".worktrees", "feature")
	mcpLinkedWorktree(t, root, work)
	// Open through the CLI in the worktree so this regression is independent
	// of MCP open's worktree support.
	t.Chdir(work)
	id := strings.Fields(peerLine(t, "peer", "open", "plan"))[0]
	t.Chdir(root)
	task := mcpTaskFixture(t, work) // a.go exists only in the peer's checkout.
	rel := ".duolog/tasks/" + id + "-send-1/task.md"
	line := "Read " + rel + " and do it. Do not call rex. Write the answer to " + policy.ReportPath(id) + ". Reply \"done\"."
	peerRex(t, claudeIdleScreen(), claudeIdleScreen(), claudeHolding(line), claudeAtWork())
	rex, err := exec.LookPath("rex")
	if err != nil {
		t.Fatal(err)
	}
	out := mustMCPPeer(t, mcp.PeerInput{Action: "send", ID: id, Task: task})
	if out.TaskPath != rel || out.Line != id+" sent "+rel {
		t.Fatalf("%+v", out)
	}
	got, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := taskspec.Render(taskspec.Spec(*task))
	if err != nil || string(got) != string(want) {
		t.Fatalf("task = %q, want %q (%v)", got, want, err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Fatalf("task written in lead checkout: %v", err)
	}
	sent := false
	for _, call := range rexCalls(t, rex) {
		if call[0] == "send" && strings.Contains(strings.Join(call, " "), line) {
			sent = true
		}
	}
	if !sent {
		t.Fatalf("peer did not receive %q: %v", line, rexCalls(t, rex))
	}
	// The report lies in the peer's checkout, not the lead's, and a wait from
	// the lead's checkout still returns it.
	report := filepath.Join(work, filepath.FromSlash(policy.ReportPath(id)))
	if err := os.WriteFile(report, []byte("findings\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	peerRex(t, claudeAtWork(), claudeIdleScreen(), claudeIdleScreen())
	waited := mustMCPPeer(t, mcp.PeerInput{Action: "wait", ID: id})
	if waited.Report != "findings\n" || waited.ReportPath != report {
		t.Fatalf("wait = %+v, want the report at %s", waited, report)
	}
}

func TestMCPPeerSendUnknownPeerWritesNothing(t *testing.T) {
	root, _ := mcpPeerProject(t, sequence(claudeIdleScreen()))
	_, err := mcpPeerCall(t, mcp.PeerInput{Action: "send", ID: "peer-missing", Task: mcpTaskFixture(t, root)})
	if err == nil || !strings.Contains(err.Error(), "unknown peer") {
		t.Fatalf("unknown peer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog/tasks/peer-missing")); !os.IsNotExist(err) {
		t.Fatalf("unknown peer left a task folder: %v", err)
	}
}

func TestMCPPeerSendWritesTaskFile(t *testing.T) {
	root, _ := mcpPeerProject(t, sequence(claudeIdleScreen()))
	id := mustMCPPeer(t, mcp.PeerInput{Action: "open", Role: "plan"}).ID
	rel := ".duolog/tasks/" + id + "-send-1/task.md"
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
	// Mutation killed: a refused send overwrites the active task's original path.
	changed := *task
	changed.Goal = "A different task."
	if _, err := mcpPeerCall(t, mcp.PeerInput{Action: "send", ID: id, Task: &changed}); err == nil {
		t.Fatal("busy peer accepted task")
	}
	if after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err != nil || string(after) != string(want) {
		t.Fatalf("active task overwritten: %s %v", after, err)
	}
	if _, err := mcpPeerCall(t, mcp.PeerInput{Action: "send", ID: id}); err == nil || !strings.Contains(err.Error(), "task") {
		t.Fatalf("a send without a task: %v", err)
	}
}

func TestMCPPeerSendRunHereIsField(t *testing.T) {
	root, _ := mcpPeerProject(t, sequence(claudeIdleScreen()))
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
	mcpPeerProject(t, sequence(switchModel, switchModel, switchModel, claudeIdleScreen(), claudeIdleScreen()))
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
		root, _ := mcpPeerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
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
	root, _ := mcpPeerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	out := mustMCPPeer(t, mcp.PeerInput{Action: "list"})
	want := []mcp.PeerRow{{ID: "peer-list", Role: "plan", Agent: "claude", State: "ready", Task: ".duolog/task.md"}}
	if fmt.Sprint(out.Peers) != fmt.Sprint(want) {
		t.Fatalf("%+v", out.Peers)
	}
}

func TestMCPPeerUnknownAction(t *testing.T) {
	_, rex := mcpPeerProject(t, testbin.Scenario{})
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
	_, rex := mcpPeerProject(t, testbin.Scenario{})
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

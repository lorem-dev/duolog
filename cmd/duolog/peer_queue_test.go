package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestPeerQueueAddListRemove(t *testing.T) {
	root, rex := peerProject(t, testbin.Scenario{})
	if err := os.WriteFile(filepath.Join(root, "task.md"), []byte("## Goal\nx\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "peer", "queue", "add", "implement", "--cwd", root, "--task", "task.md")
	id, rest, _ := strings.Cut(strings.TrimSpace(out), " ")
	if err != nil || !strings.HasPrefix(id, "q-") || rest != "queued" {
		t.Fatalf("add: %q %v", out, err)
	}
	out, err = executeBusCommand(t, "peer", "queue", "list")
	if err != nil || strings.TrimSpace(out) != id+" implement . task.md queued" {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err := executeBusCommand(t, "peer", "queue", "add", "implement", "--task", "../task.md"); err == nil {
		t.Fatal("a task path with .. was queued")
	}
	out, err = executeBusCommand(t, "peer", "queue", "remove", id)
	if err != nil || strings.TrimSpace(out) != id+" removed" {
		t.Fatalf("remove: %q %v", out, err)
	}
	if out, err = executeBusCommand(t, "peer", "queue", "list"); err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("list after remove: %q %v", out, err)
	}
	if calls := rexCalls(t, rex); len(calls) != 0 {
		t.Fatalf("the queue commands reached rex: %v", calls)
	}
}

// The lead's prompt hook starts its queue run, and so does a peer-idle message
// that the lead's tool hook delivers. One hook starts one run at most, and the
// run gets the lead's options.
func TestLeadHooksStartTheQueueRun(t *testing.T) {
	root := compactProject(t)
	// A peer's pane starts no run; the test may itself run in one.
	t.Setenv("DUOLOG_PEER", "")
	started := 0
	old := startQueueRun
	startQueueRun = func(ctx context.Context) (bool, error) {
		if _, err := peer.QueueList(ctx); err != nil {
			t.Errorf("the run's context names no lead: %v", err)
		}
		started++
		return true, nil
	}
	t.Cleanup(func() { startQueueRun = old })
	runHook(t, "UserPromptSubmit", `{"session_id":"lead-1","hook_event_name":"UserPromptSubmit"}`)
	if started != 1 {
		t.Fatalf("prompt started %d runs, want 1", started)
	}
	post := `{"session_id":"lead-1","hook_event_name":"PostToolUse"}`
	runHook(t, "PostToolUse", post)
	if started != 1 {
		t.Fatalf("a tool call without a peer-idle message started a run (%d)", started)
	}
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(filepath.Join(root, ".duolog"), cfg.Wire)
	if err != nil {
		t.Fatal(err)
	}
	to := session.Address(session.Lead{ID: "lead-1", Target: config.Target{Agent: "claude"}})
	for _, id := range []string{"peer-aa1111", "peer-bb2222"} {
		if _, err := b.Send(bus.Message{From: "claude/plan", To: to, K: "peer-idle", Ref: id, S: id + " idle in ."}); err != nil {
			t.Fatal(err)
		}
	}
	if out := runHook(t, "PostToolUse", post); !strings.Contains(out, "peer-idle peer-bb2222") || started != 2 {
		t.Fatalf("two peer-idle messages started %d runs in all, want 2: %q", started, out)
	}
	runHook(t, "PostToolUse", post)
	if started != 2 {
		t.Fatalf("a delivered peer-idle started another run (%d)", started)
	}
}

// peer close runs the queue after it closed the peer.
func TestPeerCloseStartsQueueRun(t *testing.T) {
	peerProject(t, sequence(claudeIdleScreen()))
	started := 0
	old := startQueueRun
	startQueueRun = func(context.Context) (bool, error) { started++; return true, nil }
	t.Cleanup(func() { startQueueRun = old })
	opened, err := executeBusCommand(t, "peer", "open", "plan")
	if err != nil {
		t.Fatal(err)
	}
	id, _, _ := strings.Cut(strings.TrimSpace(opened), " ")
	out, err := executeBusCommand(t, "peer", "close", id)
	if err != nil || strings.TrimSpace(out) != id+" closed; queue run started" || started != 1 {
		t.Fatalf("%q %v, %d runs", out, err, started)
	}
}

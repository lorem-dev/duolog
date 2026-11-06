package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

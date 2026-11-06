package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const viewClientReply = `{"clients":[{"client_id":"client:app","info":{"kind":"app","name":"Rex"},"session_ids":["sess"]}]}`

func viewRex(t *testing.T) string {
	t.Helper()
	return testbin.Install(t, "rex", testbin.Scenario{Exit: 99, Stderr: "unexpected rex command", Responses: map[string]testbin.Scenario{
		"block inspect": {Stdout: `{"block":{"window_id":"window:lead"}}`},
		"wait":          {Exit: 1, Stderr: "Error: wait timed out after 500ms: context deadline exceeded"},
		"client ls":     {Stdout: viewClientReply},
		"-C":            {Stdout: "{}"},
	}})
}

func TestPeerViewPrintsOneLine(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{})
	clearModelMarkers(t)
	asTerminal(t, true)
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-list", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:peers"}); err != nil {
		t.Fatal(err)
	}
	rex := viewRex(t)

	out, err := executeBusCommand(t, "peer", "view", "peer-list")
	if err != nil || strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("%q %v", out, err)
	}
	if want := "peer-list shown on client client:app: duolog | claude-plan"; strings.TrimSpace(out) != want {
		t.Fatalf("%q", out)
	}
	for _, c := range rexCalls(t, rex) {
		if c[0] == "capture" || c[0] == "send" || c[0] == "send-key" {
			t.Fatalf("view touched the pane: %v", c)
		}
	}
	// An explicit client skips the listing.
	before := len(rexCalls(t, rex))
	out, err = executeBusCommand(t, "peer", "view", "peer-list", "--client", "client:other")
	if err != nil || !strings.Contains(out, "shown on client client:other") {
		t.Fatalf("%q %v", out, err)
	}
	for _, c := range rexCalls(t, rex)[before:] {
		if c[0] == "client" {
			t.Fatalf("listed clients with --client: %v", c)
		}
	}
}

func TestPeerViewRefusedForWorker(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{})
	clearModelMarkers(t)
	asTerminal(t, true)
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-list", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:peers"}); err != nil {
		t.Fatal(err)
	}
	rex := viewRex(t)
	t.Setenv("DUOLOG_DEPTH", "1")
	if _, err := executeBusCommand(t, "peer", "view", "peer-list"); err == nil {
		t.Fatal("a worker viewed a peer")
	}
	if _, err := os.Stat(rex + ".calls.ndjson"); !os.IsNotExist(err) {
		t.Fatalf("rex was called: %v", rexCalls(t, rex))
	}
}

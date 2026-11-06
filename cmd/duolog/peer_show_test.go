package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func recordShowPeer(t *testing.T, root string) {
	t.Helper()
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-list", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "sess"}); err != nil {
		t.Fatal(err)
	}
}

func TestPeerShowRefusedForModels(t *testing.T) {
	root, rex := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordShowPeer(t, root)
	const refusal = "only to your own terminal"
	check := func(label string) {
		t.Helper()
		out, err := executeBusCommand(t, "peer", "show", "peer-list")
		if err == nil || !strings.Contains(err.Error(), refusal) {
			t.Fatalf("%s: %q %v", label, out, err)
		}
		if strings.Contains(out, "peer-list claude-plan") {
			t.Fatalf("%s: printed the screen: %q", label, out)
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
	clearModelMarkers(t)
	asTerminal(t, true)
	t.Setenv("DUOLOG_DEPTH", "1")
	check("worker")
	if calls := rexCalls(t, rex); len(calls) != 0 {
		t.Fatalf("rex called for a refused show: %v", calls)
	}
}

func TestPeerShowPrintsHeaderAndLines(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordShowPeer(t, root)
	clearModelMarkers(t)
	asTerminal(t, true)
	out, err := executeBusCommand(t, "peer", "show", "peer-list", "--lines", "3")
	if err != nil {
		t.Fatal(out, err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lines[0] != "peer-list claude-plan ready, last 3 lines" || len(lines) != 4 {
		t.Fatalf("%q", out)
	}
}

func TestPeerShowRejectsBadLines(t *testing.T) {
	root, rex := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordShowPeer(t, root)
	clearModelMarkers(t)
	asTerminal(t, true)
	for _, n := range []string{"0", "201"} {
		if _, err := executeBusCommand(t, "peer", "show", "peer-list", "--lines", n); err == nil || !strings.Contains(err.Error(), "--lines must be 1 to 200") {
			t.Fatalf("%s: %v", n, err)
		}
	}
	if calls := rexCalls(t, rex); len(calls) != 0 {
		t.Fatalf("rex called: %v", calls)
	}
}

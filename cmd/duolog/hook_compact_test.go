package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// compactProject is a project with a Claude lead and one open peer in a fake
// Rex.
func compactProject(t *testing.T) string {
	t.Helper()
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-1", Target: config.Target{Agent: "claude", Model: "opus", Effort: "high"}, Mode: mode.Ask, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	recordListPeer(t, root)
	return root
}

// runHook runs `duolog hook <event> --agent claude` with input on stdin.
func runHook(t *testing.T, event, input string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCommand(testDeps(t))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs([]string{"hook", event, "--agent", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

const compactKeep = "open peers"

func TestPreCompactPrintsInstructionsAndPeers(t *testing.T) {
	compactProject(t)
	out := runHook(t, "PreCompact", `{"session_id":"lead-1","hook_event_name":"PreCompact","trigger":"auto"}`)
	for _, want := range []string{compactKeep, "task file", "worktree", "asks still open", "pull requests", "peer-list "} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if json.Valid([]byte(out)) {
		t.Fatalf("PreCompact output must be plain text: %q", out)
	}
}

func TestPreCompactCapsThePeerLinesAtTwenty(t *testing.T) {
	root := compactProject(t)
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		id := "peer-extra-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := reg.Record(session.Entry{ID: id, Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:old"}); err != nil {
			t.Fatal(err)
		}
	}
	out := runHook(t, "PreCompact", `{"session_id":"lead-1"}`)
	if n := strings.Count(out, "peer-"); n != 20 {
		t.Fatalf("%d peer lines in %q", n, out)
	}
}

func TestSessionStartAfterCompactionPrintsThePeers(t *testing.T) {
	compactProject(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	out := runHook(t, "SessionStart", `{"session_id":"lead-1","source":"compact"}`)
	if !strings.Contains(out, "peer-list ") {
		t.Fatalf("no peer line in %q", out)
	}
	if out := runHook(t, "SessionStart", `{"session_id":"lead-1","source":"startup"}`); strings.Contains(out, "peer-list ") {
		t.Fatalf("a normal start printed the peers: %q", out)
	}
}

func TestCompactNotesOffPrintsNeither(t *testing.T) {
	root := compactProject(t)
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("lead:\n  compact_notes: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := runHook(t, "PreCompact", `{"session_id":"lead-1"}`); out != "" {
		t.Fatalf("PreCompact printed %q", out)
	}
	if out := runHook(t, "SessionStart", `{"session_id":"lead-1","source":"compact"}`); strings.Contains(out, "peer-list ") {
		t.Fatalf("SessionStart printed the peers: %q", out)
	}
}

func TestCompactNotesAreSilentForWorkers(t *testing.T) {
	compactProject(t)
	t.Setenv("DUOLOG_DEPTH", "1")
	if out := runHook(t, "PreCompact", `{"session_id":"lead-1"}`); out != "" {
		t.Fatalf("a worker printed %q", out)
	}
}

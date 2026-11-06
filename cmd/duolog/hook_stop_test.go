package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// duolog hook Stop in a peer's pane tells the lead that the peer stopped with
// an unfinished task, and prints nothing to the peer.
func TestHookCLIStopTellsTheLead(t *testing.T) {
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	r, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RegisterLead(session.Lead{ID: "stop-lead", Target: config.Target{Agent: "claude"}, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if err = r.Record(session.Entry{ID: "peer-abc123", Lead: "stop-lead", Agent: "codex", Role: "implement", Task: "tasks/t.md"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DUOLOG_DEPTH", "1")
	t.Setenv("DUOLOG_LEAD", "stop-lead")
	t.Setenv("DUOLOG_PEER", "peer-abc123")
	var out, stderr bytes.Buffer
	cmd := newRootCommand(testDeps(t))
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(`{"session_id":"peer-session","hook_event_name":"Stop"}`))
	cmd.SetArgs([]string{"hook", "Stop", "--agent", "codex"})
	if err = cmd.Execute(); err != nil || out.Len() != 0 || stderr.Len() != 0 {
		t.Fatal(out.String(), stderr.String(), err)
	}
	data, err := os.ReadFile(filepath.Join(r.Dir, "bus.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"s":"peer-abc123 stopped; task tasks/t.md is unfinished"`) || strings.Count(string(data), "\n") != 1 {
		t.Fatalf("bus %s", data)
	}
}

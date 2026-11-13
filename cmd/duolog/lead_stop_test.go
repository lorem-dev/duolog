package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// Exercise the installed hook command, Rex state reading and the suggested
// wait command together. The wait completes when the peer becomes ready.
func TestHookCLIStopRequiresPeerWait(t *testing.T) {
	ready := startupFixture(t, "codex-ready.txt")
	root, _ := peerProject(t, sequence("• Working (3s • esc to interrupt)\n"+ready, ready, ready))
	installHere(t, root)
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-1", Target: config.Target{Agent: "claude"}, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-busy", Lead: "lead-1", Agent: "codex", Role: "implement", Block: "block:peer", RexSession: "sess"}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	cmd := newRootCommand(testDeps(t))
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(`{"session_id":"lead-1","hook_event_name":"Stop","stop_hook_active":false}`))
	cmd.SetArgs([]string{"hook", "Stop", "--agent", "claude"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var decision struct{ Decision, Reason string }
	if err := json.Unmarshal(out.Bytes(), &decision); err != nil || decision.Decision != "block" || !strings.Contains(decision.Reason, "duolog peer wait peer-busy --lead lead-1") || stderr.Len() != 0 {
		t.Fatalf("Stop did not keep the lead active: %s %s %v", out.String(), stderr.String(), err)
	}
	out.Reset()
	cmd = newRootCommand(testDeps(t))
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	// An already settled ask needs no further wait. New asks still matter.
	cmd.SetArgs([]string{"peer", "wait", "peer-busy", "--lead", "lead-1", "--after-ask", "1", "--timeout", "10s"})
	if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), "peer-busy ready") || stderr.Len() != 0 {
		t.Fatalf("background wait could not finish: %s %s %v", out.String(), stderr.String(), err)
	}
}

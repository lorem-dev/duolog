package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/gate"
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
	withAgentAncestor(t, "")
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

// withAgentAncestor makes the process walk of peer show find name, or no
// agent when name is empty, so the suite reads the same inside an agent.
func withAgentAncestor(t *testing.T, name string) {
	t.Helper()
	testDeps(t).AgentAncestor = func() string { return name }
}

// A Claude or Codex process above peer show refuses it even when the
// markers are gone and stdout is a terminal, as under script or a new pane.
func TestPeerShowRefusedUnderAnAgentProcess(t *testing.T) {
	root, rex := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordShowPeer(t, root)
	clearModelMarkers(t)
	asTerminal(t, true)
	for _, agent := range []string{"claude", "codex"} {
		withAgentAncestor(t, agent)
		out, err := executeBusCommand(t, "peer", "show", "peer-list")
		if err == nil || !strings.Contains(err.Error(), "only to your own terminal") || !strings.Contains(err.Error(), agent) {
			t.Fatalf("%s: %q %v", agent, out, err)
		}
	}
	if calls := rexCalls(t, rex); len(calls) != 0 {
		t.Fatalf("rex called for a refused show: %v", calls)
	}
}

// A lead's tool call that names peer show, behind any wrapper, is asked
// (Claude) or denied (Codex) before it runs. Other peer commands pass.
func TestPeerShowLeadGuard(t *testing.T) {
	root, _ := telemetryCommandSetup(t)
	stateDir := filepath.Join(root, ".duolog")
	guarded := []string{
		"duolog peer show peer-x",
		"duolog peer show peer-x --lines 200",
		"env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT script -q /dev/null duolog peer show peer-x",
		"script -qc 'duolog peer show peer-x' /dev/null",
		"env -u CODEX_THREAD_ID -u CODEX_SANDBOX -u CODEX_SANDBOX_NETWORK_DISABLED -u DUOLOG_DEPTH duolog peer show peer-x",
		"rex split -s sess --shell=none -- duolog peer show peer-x",
		`tmux new-window "duolog peer show peer-x"`,
		`sh -c "duolog peer show peer-x"`,
		"bash -lc 'cd /tmp && duolog peer show peer-x'",
		"duolog --lead L peer show peer-x",
		"duolog peer --lead=L show peer-x",
		"true&&scripts/dev/bin/duolog peer show peer-x",
		"duolog peer list; duolog.exe peer show peer-x",
		"echo $(duolog peer show peer-x)",
	}
	open := []string{
		"duolog peer list --long", "duolog peer view peer-x", "duolog peer status peer-x",
		"duolog peer list --json | jq .", "go test ./cmd/duolog", "rex capture -b block:peer",
	}
	for _, agent := range []string{"claude", "codex"} {
		for _, command := range guarded {
			d, ok := leadGuard(stateDir, agent, leadToolInput(agent, root, "Bash", map[string]string{"command": command}))
			if !ok {
				t.Errorf("%s %q passed the lead guard", agent, command)
				continue
			}
			want := map[string]string{"claude": gate.Ask, "codex": gate.Deny}[agent]
			if d.Kind != want || !strings.Contains(d.Reason, "peer show") {
				t.Errorf("%s %q: %+v", agent, command, d)
			}
		}
		for _, command := range open {
			if d, ok := leadGuard(stateDir, agent, leadToolInput(agent, root, "Bash", map[string]string{"command": command})); ok {
				t.Errorf("%s %q guarded: %+v", agent, command, d)
			}
		}
	}
}

func TestPeerShowPrintsHeaderAndLines(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordShowPeer(t, root)
	clearModelMarkers(t)
	asTerminal(t, true)
	withAgentAncestor(t, "")
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
	withAgentAncestor(t, "")
	for _, n := range []string{"0", "201"} {
		if _, err := executeBusCommand(t, "peer", "show", "peer-list", "--lines", n); err == nil || !strings.Contains(err.Error(), "--lines must be 1 to 200") {
			t.Fatalf("%s: %v", n, err)
		}
	}
	if calls := rexCalls(t, rex); len(calls) != 0 {
		t.Fatalf("rex called: %v", calls)
	}
}

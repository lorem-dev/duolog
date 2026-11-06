package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/session"
)

const (
	claudeLeadID = "0f1e2d3c-claude-lead"
	codexLeadID  = "019a0b1c-codex-lead"
)

// leadSession gives a test a project folder and a fake agent session: no
// agent variable is inherited, HOME is a temporary folder, and the agent
// process is this test process.
func leadSession(t *testing.T) string {
	t.Helper()
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "CLAUDECODE", "CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED"} {
		t.Setenv(key, "")
	}
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	previous := leadAgentPID
	leadAgentPID = os.Getpid
	t.Cleanup(func() { leadAgentPID = previous })
	return root
}

func writeTranscript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// claudeTranscript writes a Claude transcript whose last prompt ran in
// permissionMode.
func claudeTranscript(t *testing.T, home, permissionMode string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "projects", "-work-app", claudeLeadID+".jsonl")
	writeTranscript(t, path, `{"type":"user","permissionMode":"`+permissionMode+`"}`+"\n"+
		`{"type":"assistant","message":{"model":"claude-sonnet-5-5"}}`+"\n")
	return path
}

// codexRollout writes a Codex rollout for an interactive thread in the given
// sandbox and approval policy.
func codexRollout(t *testing.T, home, originator, sandbox, approval string) string {
	t.Helper()
	path := filepath.Join(home, "sessions", "2026", "11", "05", "rollout-2026-11-05T10-00-00-"+codexLeadID+".jsonl")
	writeTranscript(t, path, `{"type":"session_meta","payload":{"originator":"`+originator+`"}}`+"\n"+
		`{"type":"turn_context","payload":{"model":"gpt-6.1-sol","effort":"high","approval_policy":"`+approval+`","sandbox_policy":{"type":"`+sandbox+`"}}}`+"\n")
	return path
}

func registeredLead(t *testing.T, root, id string) (session.Lead, mode.Mode, bool) {
	t.Helper()
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	lead, ok, err := reg.Lead(id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return lead, "", false
	}
	_, effective, err := reg.EffectiveMode(id)
	if err != nil {
		t.Fatal(err)
	}
	return lead, effective, true
}

func TestLeadRegisterClaudeCannotRaiseAboveObservedMode(t *testing.T) {
	root := leadSession(t)
	transcript := claudeTranscript(t, os.Getenv("HOME"), "acceptEdits")
	t.Setenv("CLAUDE_CODE_SESSION_ID", claudeLeadID)
	if _, err := executeBusCommand(t, "lead", "register", "full"); err == nil || !strings.Contains(err.Error(), "above the observed mode edits") {
		t.Fatalf("full above edits: %v", err)
	}
	if _, _, ok := registeredLead(t, root, claudeLeadID); ok {
		t.Fatal("a refused registration recorded the lead")
	}
	out, err := executeBusCommand(t, "lead", "register", "edits")
	if err != nil || !strings.Contains(out, "claude/lead.0f1e2d registered as the lead: process") || !strings.HasSuffix(out, "mode edits\n") {
		t.Fatalf("%q %v", out, err)
	}
	lead, effective, ok := registeredLead(t, root, claudeLeadID)
	if !ok || lead.PID != os.Getpid() || lead.Agent != "claude" || effective != mode.Edits || lead.TranscriptPath != transcript || lead.Model != "claude-sonnet-5-5" {
		t.Fatalf("%+v %s %v", lead, effective, ok)
	}
	// A lower stated mode is a ceiling, as duolog mode sets it.
	if _, err := executeBusCommand(t, "lead", "register", "plan"); err != nil {
		t.Fatal(err)
	}
	if _, effective, _ := registeredLead(t, root, claudeLeadID); effective != mode.Plan {
		t.Fatalf("mode %s, want plan", effective)
	}
}

func TestLeadRegisterWithoutObservationStaysAtAsk(t *testing.T) {
	root := leadSession(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", claudeLeadID)
	if _, err := executeBusCommand(t, "lead", "register", "edits"); err == nil || !strings.Contains(err.Error(), "cannot observe") {
		t.Fatalf("edits with no observation: %v", err)
	}
	if out, err := executeBusCommand(t, "lead", "register"); err != nil || !strings.HasSuffix(out, "mode ask\n") {
		t.Fatalf("%q %v", out, err)
	}
	if lead, _, ok := registeredLead(t, root, claudeLeadID); !ok || lead.PID != os.Getpid() {
		t.Fatalf("%+v %v", lead, ok)
	}
}

func TestLeadRegisterCodexUsesRolloutMode(t *testing.T) {
	root := leadSession(t)
	codexHome := filepath.Join(os.Getenv("HOME"), "codex-home")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CODEX_THREAD_ID", codexLeadID)
	codexRollout(t, codexHome, "codex_exec", "workspace-write", "on-request")
	if _, err := executeBusCommand(t, "lead", "register"); err == nil || !strings.Contains(err.Error(), "interactive Codex") {
		t.Fatalf("non-interactive thread: %v", err)
	}
	rollout := codexRollout(t, codexHome, "codex-tui", "workspace-write", "on-request")
	if _, err := executeBusCommand(t, "lead", "register", "full"); err == nil || !strings.Contains(err.Error(), "above the observed mode edits") {
		t.Fatalf("full above edits: %v", err)
	}
	if out, err := executeBusCommand(t, "lead", "register"); err != nil || !strings.Contains(out, "codex/lead.019a0b") || !strings.HasSuffix(out, "mode edits\n") {
		t.Fatalf("%q %v", out, err)
	}
	lead, effective, ok := registeredLead(t, root, codexLeadID)
	if !ok || lead.PID != os.Getpid() || effective != mode.Edits || lead.TranscriptPath != rollout || lead.Effort != "high" {
		t.Fatalf("%+v %s %v", lead, effective, ok)
	}
}

func TestLeadRegisterRefusedForWorker(t *testing.T) {
	root := leadSession(t)
	claudeTranscript(t, os.Getenv("HOME"), "acceptEdits")
	t.Setenv("CLAUDE_CODE_SESSION_ID", claudeLeadID)
	t.Setenv("DUOLOG_DEPTH", "1")
	if _, err := executeBusCommand(t, "lead", "register", "ask"); err == nil || !strings.Contains(err.Error(), "workers cannot register a lead") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog")); !os.IsNotExist(err) {
		t.Fatalf("a worker created project state: %v", err)
	}
}

func TestLeadRegisterNeedsTheSessionID(t *testing.T) {
	leadSession(t)
	if _, err := executeBusCommand(t, "lead", "register"); err == nil || !strings.Contains(err.Error(), "inside a Claude Code or Codex session") {
		t.Fatalf("outside an agent: %v", err)
	}
	t.Setenv("CLAUDECODE", "1")
	if _, err := executeBusCommand(t, "lead", "register"); err == nil || !strings.Contains(err.Error(), "does not set CLAUDE_CODE_SESSION_ID") {
		t.Fatalf("claude without an ID: %v", err)
	}
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CODEX_SANDBOX", "seatbelt")
	if _, err := executeBusCommand(t, "lead", "register"); err == nil || !strings.Contains(err.Error(), "does not set CODEX_THREAD_ID") {
		t.Fatalf("codex without an ID: %v", err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", claudeLeadID)
	t.Setenv("CODEX_THREAD_ID", codexLeadID)
	if _, err := executeBusCommand(t, "lead", "register"); err == nil || !strings.Contains(err.Error(), "--agent") {
		t.Fatalf("both IDs: %v", err)
	}
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "../x")
	if _, err := executeBusCommand(t, "lead", "register"); err == nil || !strings.Contains(err.Error(), "never has") {
		t.Fatalf("bad ID: %v", err)
	}
}

// The child half of TestLeadRegisterFindsAgentProcessThenPeerOpen: a real
// shell runs it, as an agent's shell tool runs duolog.
func TestLeadRegisterChild(t *testing.T) {
	if os.Getenv("DUOLOG_TEST_LEAD_REGISTER") != "1" {
		t.Skip("run by TestLeadRegisterFindsAgentProcessThenPeerOpen")
	}
	out, err := executeBusCommand(t, "lead", "register", "ask")
	if err != nil {
		t.Fatalf("%q %v", out, err)
	}
}

// A Codex session that started elsewhere registers from its shell with the
// agent's own process, and peer open then finds that lead.
func TestLeadRegisterFindsAgentProcessThenPeerOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	root, _ := peerProjectWithoutLead(t, sequence(claudeIdleScreen()))
	if _, err := executeBusCommand(t, "peer", "open", "plan"); err == nil || !strings.Contains(err.Error(), "duolog lead register") {
		t.Fatalf("no lead: %v", err)
	}
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	codexRollout(t, codexHome, "codex-tui", "workspace-write", "on-request")
	// "; exit" keeps the shell alive as the command's parent, so the walk
	// must pass it to reach this test process, the stand-in agent.
	child := exec.Command("/bin/sh", "-c", `"$0" -test.run='^TestLeadRegisterChild$' -test.v; exit $?`, os.Args[0])
	child.Dir = root
	child.Env = append(os.Environ(), "DUOLOG_TEST_LEAD_REGISTER=1", "CODEX_HOME="+codexHome, "CODEX_THREAD_ID="+codexLeadID,
		"CLAUDE_CODE_SESSION_ID=", "CLAUDECODE=", "DUOLOG_DEPTH=", "DUOLOG_LEAD=")
	if out, err := child.CombinedOutput(); err != nil || !strings.Contains(string(out), "--- PASS: TestLeadRegisterChild") {
		t.Fatalf("%s %v", out, err)
	}
	lead, effective, ok := registeredLead(t, root, codexLeadID)
	if !ok || lead.PID != os.Getpid() || effective != mode.Ask {
		t.Fatalf("lead %+v mode %s live %v; want process %d", lead, effective, ok, os.Getpid())
	}
	out, err := executeBusCommand(t, "peer", "open", "plan")
	if err != nil || !strings.HasPrefix(out, "peer-") {
		t.Fatalf("%q %v", out, err)
	}
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := reg.Entries()
	if err != nil || len(entries) != 1 || entries[0].Lead != codexLeadID {
		t.Fatalf("%+v %v", entries, err)
	}
}

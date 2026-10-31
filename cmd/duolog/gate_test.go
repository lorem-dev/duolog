package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
)

func gateSetup(t *testing.T, m mode.Mode) (*session.Registry, string) {
	t.Helper()
	isolatedRouteConfig(t)
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("DUOLOG_DEPTH", "1")
	t.Setenv("DUOLOG_LEAD", "lead-123456")
	t.Setenv("DUOLOG_ROLE", "implement")
	r, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.RegisterLead(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "codex"}, Mode: m}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".duolog/config.yaml"), []byte("policy:\n  ask_timeout_seconds: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return r, root
}
func gateCommand(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}
func runGate(t *testing.T, command string) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": command}})
	out, err := gateCommand(t, string(data), "gate", "--agent", "codex", "--event", "PreToolUse")
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestLoweredModeAppliesNextCall(t *testing.T) {
	r, _ := gateSetup(t, mode.Full)
	if out := runGate(t, "custom-command"); out != "" {
		t.Fatal(out)
	}
	if err := r.RegisterLead(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "codex"}, Mode: mode.Ask}); err != nil {
		t.Fatal(err)
	}
	if out := runGate(t, "custom-command"); !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "ask") {
		t.Fatal(out)
	}
}
func TestModeCommandCannotRaise(t *testing.T) {
	gateSetup(t, mode.Ask)
	t.Setenv("DUOLOG_DEPTH", "")
	_, err := gateCommand(t, "", "mode", "full")
	if err == nil || !strings.Contains(err.Error(), "ask") {
		t.Fatal(err)
	}
}
func TestModeCommandCanLower(t *testing.T) {
	r, _ := gateSetup(t, mode.Full)
	t.Setenv("DUOLOG_DEPTH", "")
	if _, err := gateCommand(t, "", "mode", "plan"); err != nil {
		t.Fatal(err)
	}
	if out, err := gateCommand(t, "", "mode"); err != nil || strings.TrimSpace(out) != "plan" {
		t.Fatal(out, err)
	}
	// A fresh lead observation must not discard the user's lower ceiling.
	if err := r.RegisterLead(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "codex"}, Mode: mode.Full}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DUOLOG_DEPTH", "2")
	if out := runGate(t, "go test ./..."); !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "plan") {
		t.Fatal(out)
	}
}
func TestModeRefusedForWorker(t *testing.T) {
	gateSetup(t, mode.Full)
	if _, err := gateCommand(t, "", "mode", "plan"); err == nil {
		t.Fatal("worker changed mode")
	}
}
func TestLeadSessionIsNeverBlocked(t *testing.T) {
	_, root := gateSetup(t, mode.Plan)
	for _, depth := range []string{"", "0", "-1"} {
		t.Setenv("DUOLOG_DEPTH", depth)
		if out, err := gateCommand(t, "bad input", "gate", "--agent", "codex", "--event", "PreToolUse"); out != "" || err != nil {
			t.Fatal(out, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog/decisions.ndjson")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
func TestBrokenConfigFailsClosedForWorker(t *testing.T) {
	_, root := gateSetup(t, mode.Full)
	if err := os.WriteFile(filepath.Join(root, ".duolog/config.yaml"), []byte("policy: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if out := runGate(t, "go test ./..."); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatal(out)
	}
}
func TestBrokenConfigFailsOpenForLead(t *testing.T) {
	_, root := gateSetup(t, mode.Full)
	t.Setenv("DUOLOG_DEPTH", "")
	if err := os.WriteFile(filepath.Join(root, ".duolog/config.yaml"), []byte("policy: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if out := runGate(t, "git push"); out != "" {
		t.Fatal(out)
	}
}
func TestGateLogsWithoutExtraStdout(t *testing.T) {
	_, root := gateSetup(t, mode.Edits)
	if out := runGate(t, "go test ./..."); out != "" {
		t.Fatal(out)
	}
	if out := runGate(t, "git push"); !json.Valid([]byte(out)) {
		t.Fatal(out)
	}
	if out := runGate(t, "custom-command"); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatal(out)
	}
	data, err := os.ReadFile(filepath.Join(root, ".duolog/decisions.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(strings.TrimSpace(string(data)), "\n")) != 3 {
		t.Fatal(string(data))
	}
	out, err := gateCommand(t, "", "decisions")
	if err != nil || !strings.Contains(out, "Allow") || !strings.Contains(out, "Deny") || !strings.Contains(out, "ask timeout") || !strings.Contains(out, "Reason") {
		t.Fatal(out, err)
	}
}
func TestGateUsesLeadAgentAndRejectsMalformedSettings(t *testing.T) {
	_, root := gateSetup(t, mode.Full)
	for _, name := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".claude/settings.json"), []byte("bad json"), 0600); err != nil {
		t.Fatal(err)
	}
	if out := runGate(t, "go test ./..."); out != "" {
		t.Fatal(out)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex/config.toml"), []byte("sandbox_mode = ["), 0600); err != nil {
		t.Fatal(err)
	}
	if out := runGate(t, "go test ./..."); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatal(out)
	}
}
func TestGateMissingLeadFailsClosed(t *testing.T) {
	gateSetup(t, mode.Full)
	t.Setenv("DUOLOG_LEAD", "unknown")
	if out := runGate(t, "go test ./..."); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatal(out)
	}
}

func TestGateRebasesPathsFromHookCwd(t *testing.T) {
	_, root := gateSetup(t, mode.Full)
	for _, target := range []string{filepath.Join(root, ".duolog/x"), "../.duolog/x"} {
		data, _ := json.Marshal(map[string]any{"tool_name": "Write", "cwd": filepath.Join(root, "src"), "tool_input": map[string]string{"file_path": target}})
		out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", "PreToolUse")
		if err != nil || !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Fatal(out, err)
		}
	}
}

func TestGateReadsLeadTranscriptAtDecisionTime(t *testing.T) {
	r, root := gateSetup(t, mode.Full)
	l, _, err := r.Lead("lead-123456")
	if err != nil {
		t.Fatal(err)
	}
	l.TranscriptPath = filepath.Join(root, "lead.jsonl")
	if err = r.RegisterLead(l); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(l.TranscriptPath, []byte("{\"type\":\"turn_context\",\"payload\":{\"sandbox_policy\":{\"type\":\"read-only\"}}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out := runGate(t, "go test ./..."); !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "plan") {
		t.Fatal(out)
	}
}

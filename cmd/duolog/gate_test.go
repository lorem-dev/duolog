package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
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

// shortAskWait makes an unanswered ask time out after 50 ms instead of the
// configured second. Only a test that never answers the ask may use it: an
// answer could come after the wait.
func shortAskWait(t *testing.T) {
	t.Helper()
	old := askWait
	askWait = func(int) time.Duration { return 50 * time.Millisecond }
	t.Cleanup(func() { askWait = old })
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

// A peer in a linked worktree writes its report under that worktree, the path
// peer status reads. The state folder of the main checkout stays denied.
func TestPeerReportWriteInLinkedWorktree(t *testing.T) {
	_, root := gateSetup(t, mode.Full)
	work := t.TempDir()
	git := filepath.Join(root, ".git")
	for _, dir := range []string{"objects", "refs/heads", "worktrees/feature"} {
		if err := os.MkdirAll(filepath.Join(git, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, text := range map[string]string{
		filepath.Join(git, "HEAD"):                        "ref: refs/heads/main\n",
		filepath.Join(git, "config"):                      "[core]\nrepositoryformatversion=0\nbare=false\n",
		filepath.Join(git, "worktrees/feature/HEAD"):      "ref: refs/heads/feature\n",
		filepath.Join(git, "worktrees/feature/commondir"): "../..\n",
		filepath.Join(git, "worktrees/feature/gitdir"):    filepath.Join(work, ".git") + "\n",
		filepath.Join(work, ".git"):                       "gitdir: " + filepath.Join(git, "worktrees/feature") + "\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(work)
	t.Setenv("DUOLOG_PEER", "peer-abc123")
	write := func(path string) string {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"session_id": "worker", "cwd": work, "tool_name": "Write", "tool_input": map[string]string{"file_path": path, "content": "findings"}})
		out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", "PreToolUse")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := write(filepath.Join(work, ".duolog", "tasks", "peer-abc123", "report.md")); strings.Contains(out, "deny") {
		t.Fatal("own report in the worktree denied:", out)
	}
	for _, path := range []string{
		filepath.Join(root, ".duolog", "tasks", "peer-abc123", "report.md"),
		filepath.Join(work, ".duolog", "tasks", "peer-zzz999", "report.md"),
	} {
		if out := write(path); !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Fatal(path, out)
		}
	}
}

func TestWorkerRecursiveGrepOfStateDenied(t *testing.T) {
	gateSetup(t, mode.Full)
	if out := runGate(t, "grep -r token ."); !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "duolog state") {
		t.Fatal(out)
	}
}

func TestLeadGateAsksBeforePersonOnlyAllow(t *testing.T) {
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	dir := personOnlyBus(t)
	t.Chdir(filepath.Dir(dir))
	for _, tc := range []struct{ command, decision string }{
		{"duolog allow 1", `"permissionDecision":"ask"`},
		{"go test ./...", ""},
		{"duolog allow 2", ""},
		{"duolog deny 1", ""},
	} {
		out, err := gateCommand(t, string(bashInput("claude", tc.command)), "gate", "--agent", "claude", "--event", "PreToolUse")
		if err != nil || tc.decision == "" && out != "" || tc.decision != "" && !strings.Contains(out, tc.decision) {
			t.Fatal(tc.command, out, err)
		}
	}
	out, err := gateCommand(t, string(bashInput("codex", "duolog allow 1")), "gate", "--agent", "codex", "--event", "PreToolUse")
	if err != nil || !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "own terminal") {
		t.Fatal(out, err)
	}
}

type leadGuardErrorWriter struct{ writes int }

type leadGuardOrderWriter struct {
	bytes.Buffer
	before func()
}

func (w *leadGuardOrderWriter) Write(data []byte) (int, error) {
	w.before()
	return w.Buffer.Write(data)
}

func TestLeadGuardWritesBeforeObservation(t *testing.T) {
	r, root := gateSetup(t, mode.Ask)
	t.Setenv("DUOLOG_DEPTH", "")
	if err := r.RegisterLead(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "claude"}, Mode: mode.Ask}); err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(filepath.Join(root, ".duolog"), config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Send(bus.Message{K: "ask", P: true}); err != nil {
		t.Fatal(err)
	}
	w := &leadGuardOrderWriter{before: func() {
		lead, _, err := r.Lead("lead-123456")
		if err != nil || lead.Mode != mode.Ask {
			t.Error("observation ran before guard output", lead.Mode, err)
		}
	}}
	cmd := newRootCommand()
	cmd.SetOut(w)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(`{"session_id":"lead-123456","permission_mode":"bypassPermissions","tool_name":"Bash","tool_input":{"command":"duolog allow 1"}}`))
	cmd.SetArgs([]string{"gate", "--agent", "claude", "--event", "PreToolUse"})
	if err := cmd.Execute(); err != nil || w.Len() == 0 {
		t.Fatal(err, w.String())
	}
	lead, _, err := r.Lead("lead-123456")
	if err != nil || lead.Mode != mode.Full {
		t.Fatal("observation must still run", lead.Mode, err)
	}
}

func (w *leadGuardErrorWriter) Write(p []byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}

func TestLeadGateIgnoresGuardWriteError(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			isolatedRouteConfig(t)
			t.Setenv("DUOLOG_DEPTH", "")
			dir := personOnlyBus(t)
			t.Chdir(filepath.Dir(dir))
			cmd := newRootCommand()
			writer := &leadGuardErrorWriter{}
			cmd.SetOut(writer)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetIn(strings.NewReader(string(bashInput(agent, "duolog allow 1"))))
			cmd.SetArgs([]string{"gate", "--agent", agent, "--event", "PreToolUse"})
			if err := cmd.Execute(); err != nil || writer.writes == 0 {
				t.Fatal("lead hook must succeed after attempting output:", writer.writes, err)
			}
		})
	}
}

func TestWorkerAskPersonOnly(t *testing.T) {
	outside := filepath.ToSlash(filepath.Join(t.TempDir(), "outside"))
	for _, tc := range []struct {
		name, tool string
		input      map[string]string
		person     bool
	}{
		{"outside-last", "apply_patch", map[string]string{"patch": "*** Begin Patch\n*** Update File: inside.txt\n*** Update File: ../outside.txt\n*** End Patch"}, true},
		{"outside-first", "apply_patch", map[string]string{"patch": "*** Begin Patch\n*** Update File: ../outside.txt\n*** Update File: inside.txt\n*** End Patch"}, true},
		{"inside-only", "apply_patch", map[string]string{"patch": "*** Begin Patch\n*** Update File: inside.txt\n*** End Patch"}, false},
		{"read-command", "Bash", map[string]string{"command": "cat ../outside.txt"}, true},
		{"copy-outside", "Bash", map[string]string{"command": "cp ~/.ssh/k k"}, true},
		{"archive-outside", "Bash", map[string]string{"command": "tar czf x.tgz ~/notes"}, true},
		{"git-outside", "Bash", map[string]string{"command": "git -C " + outside + " log"}, true},
		{"discard-test-output", "Bash", map[string]string{"command": "go test ./... >/dev/null"}, false},
		{"discard-make-output", "Bash", map[string]string{"command": "make test > /dev/null 2>&1"}, false},
		{"absolute-program", "Bash", map[string]string{"command": "/usr/bin/env go test"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := gateSetup(t, mode.Ask)
			shortAskWait(t)
			data, _ := json.Marshal(map[string]any{"tool_name": tc.tool, "cwd": root, "tool_input": tc.input})
			if _, err := gateCommand(t, string(data), "gate", "--agent", "codex", "--event", "PreToolUse"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, ".duolog", "bus.ndjson"))
			if err != nil {
				t.Fatal(err)
			}
			var message bus.Message
			if err := json.Unmarshal(bytes.TrimSpace(data), &message); err != nil {
				t.Fatal(err)
			}
			if message.K != "ask" || message.P != tc.person || bytes.Contains(data, []byte(`"p":true`)) != tc.person {
				t.Fatalf("person=%v: %s", tc.person, data)
			}
		})
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

// One answer covers a whole patch, so the question and its log name every path.
func TestMultiPathPatchAsksAboutEveryPath(t *testing.T) {
	_, root := gateSetup(t, mode.Ask)
	patch := "*** Begin Patch\n*** Update File: a.go\n*** Update File: ../../home/.zshrc\n*** End Patch"
	data, _ := json.Marshal(map[string]any{"tool_name": "apply_patch", "cwd": root, "tool_input": map[string]string{"patch": patch}})
	if _, err := gateCommand(t, string(data), "gate", "--agent", "codex", "--event", "PreToolUse"); err != nil {
		t.Fatal(err)
	}
	logged, err := os.ReadFile(filepath.Join(root, ".duolog", "requests.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"a.go", "home/.zshrc"} {
		if !bytes.Contains(logged, []byte(path)) {
			t.Fatalf("%s missing from request log: %s", path, logged)
		}
	}
}

func TestWorkerGateCreatesNoStateInAnUnrelatedFolder(t *testing.T) {
	gateSetup(t, mode.Full)
	empty := t.TempDir()
	t.Chdir(empty)
	if out := runGate(t, "go test ./..."); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatal(out)
	}
	if _, err := os.Stat(filepath.Join(empty, ".duolog")); !os.IsNotExist(err) {
		t.Fatal("the gate created state", err)
	}
}

func TestHookInputMayStartWithAByteOrderMark(t *testing.T) {
	gateSetup(t, mode.Full)
	data, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": "git status"}})
	out, err := gateCommand(t, "\xef\xbb\xbf"+string(data), "gate", "--agent", "codex", "--event", "PreToolUse")
	if err != nil || out != "" {
		t.Fatalf("%q %v", out, err)
	}
}

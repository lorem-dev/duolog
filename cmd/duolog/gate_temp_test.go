package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/mode"
)

// A Claude worker reads its own session temp folder, which the hook places
// from the session id in its input, without asking the person. Another
// session's folder, a .. and a symlink out of the folder still ask, and an
// unanswered ask denies.
func TestWorkerReadsOwnClaudeTemp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Claude's session temp folders are a POSIX layout")
	}
	_, root := gateSetup(t, mode.Full)
	shortAskWait(t)
	base := t.TempDir()
	old := claudeTempBase
	claudeTempBase = base
	t.Cleanup(func() { claudeTempBase = old })
	const session, other = "0f1e2d3c-4b5a-4968-8778-695a4b3c2d1e", "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
	own := gate.ClaudeTemp(base, os.Getuid(), session, root)
	if own == "" {
		t.Fatal("no temp folder for the session")
	}
	outside := t.TempDir()
	for _, dir := range []string{filepath.Join(own, "scratchpad"), filepath.Join(filepath.Dir(own), other, "scratchpad")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(own, "links"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(own, "links", "esc")); err != nil {
		t.Fatal(err)
	}
	call := func(sessionID, tool string, input map[string]string) string {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"session_id": sessionID, "cwd": root, "tool_name": tool, "tool_input": input})
		out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", "PreToolUse")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	file := filepath.Join(own, "scratchpad", "out.txt")
	for _, c := range []struct {
		tool  string
		input map[string]string
	}{
		{"Read", map[string]string{"file_path": file}},
		{"Grep", map[string]string{"pattern": "x", "path": filepath.Join(own, "scratchpad")}},
		{"Bash", map[string]string{"command": "cat " + file}},
	} {
		if out := call(session, c.tool, c.input); out != "" {
			t.Errorf("own %s %v: %s", c.tool, c.input, out)
		}
	}
	for _, c := range []struct {
		session, tool string
		input         map[string]string
	}{
		{other, "Read", map[string]string{"file_path": file}},
		{session, "Read", map[string]string{"file_path": filepath.Join(filepath.Dir(own), other, "scratchpad", "out.txt")}},
		{session, "Read", map[string]string{"file_path": own + "/scratchpad/../scratchpad/out.txt"}},
		{session, "Read", map[string]string{"file_path": filepath.Join(own, "links", "esc", "secret")}},
		{session, "Read", map[string]string{"file_path": filepath.Dir(own)}},
		{session, "Grep", map[string]string{"pattern": "x", "path": filepath.Join(own, "links")}},
	} {
		if out := call(c.session, c.tool, c.input); !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Errorf("%s %s %v: %s", c.session, c.tool, c.input, out)
		}
	}
	// Exec commands other than named reads are unchanged: one of a file in the
	// temp folder gets the answer one of any outside file gets.
	for _, command := range []string{"python3 %s", "cp %s copy.txt"} {
		own := call(session, "Bash", map[string]string{"command": fmt.Sprintf(command, file)})
		elsewhere := call(session, "Bash", map[string]string{"command": fmt.Sprintf(command, filepath.Join(outside, "secret"))})
		if own != elsewhere {
			t.Errorf("%s: own temp %q, outside %q", command, own, elsewhere)
		}
	}
}

// The lead's guard for the private usage stores holds for the commands that
// two security reviews found could slip past a dropped parser change.
func TestLeadTelemetryGuardHoldsForReviewCommands(t *testing.T) {
	root, _ := telemetryCommandSetup(t)
	stateDir := filepath.Join(root, ".duolog")
	for _, command := range []string{
		`echo 'duolog telemetry show' | sh`,
		`sh <<< 'duolog telemetry show'`,
		`GIT_EDITOR='duolog telemetry show; true' git commit --allow-empty`,
		`printf 'duolog balance reset' | bash`,
		`GIT_EDITOR='duolog telemetry show;:' git commit --allow-empty`,
		`EDITOR='duolog telemetry show;:' git tag -a v1`,
	} {
		d, ok := leadGuard(stateDir, "claude", leadToolInput("claude", root, "Bash", map[string]string{"command": command}))
		if !ok || d.Kind != gate.Ask {
			t.Errorf("%s: %+v %v", command, d, ok)
		}
	}
}

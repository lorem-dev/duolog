package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

// A read-only worker reads the paths outside the project that its task lists
// under Read, which delegate hands its gate in DUOLOG_READ. A path the hook
// was given with .., a write and a path the list does not name still go to
// the lead, and an unanswered ask denies.
func TestWorkerReadsTaskReadPaths(t *testing.T) {
	_, root := gateSetup(t, mode.Ask)
	shortAskWait(t)
	t.Setenv("DUOLOG_ROLE", "review_final")
	outside := pathutil.Resolved(t.TempDir())
	docs := filepath.Join(outside, "docs")
	for _, file := range []string{filepath.Join(docs, "a.md"), filepath.Join(outside, "other.md")} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	call := func(tool string, input map[string]string) string {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"session_id": "s1", "cwd": root, "tool_name": tool, "tool_input": input})
		out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", "PreToolUse")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	file := filepath.Join(docs, "a.md")
	allowed := []struct {
		tool  string
		input map[string]string
	}{
		{"Read", map[string]string{"file_path": file}},
		{"Grep", map[string]string{"pattern": "x", "path": docs}},
		{"Bash", map[string]string{"command": "wc -l " + filepath.ToSlash(file)}},
	}
	denied := []struct {
		tool  string
		input map[string]string
	}{
		{"Read", map[string]string{"file_path": docs + "/../docs/a.md"}},
		{"Read", map[string]string{"file_path": filepath.Join(outside, "other.md")}},
		{"Write", map[string]string{"file_path": file, "content": "y"}},
		{"Bash", map[string]string{"command": "cp " + filepath.ToSlash(file) + " a.md"}},
	}
	for _, c := range append(allowed, denied...) {
		if out := call(c.tool, c.input); !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Errorf("no Read list: %s %v: %s", c.tool, c.input, out)
		}
	}
	t.Setenv(policy.TaskReadsEnv, policy.TaskReadsValue([]string{docs}))
	for _, c := range allowed {
		if out := call(c.tool, c.input); out != "" {
			t.Errorf("%s %v: %s", c.tool, c.input, out)
		}
	}
	for _, c := range denied {
		if out := call(c.tool, c.input); !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Errorf("%s %v: %s", c.tool, c.input, out)
		}
	}
	// The gate refuses an entry too wide to hand over, as delegate does, after
	// it resolves the entry's symlinks: a folder of credentials, also through
	// a link to it, and the home folder.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(ssh, "config")
	if err := os.WriteFile(key, []byte("Host x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := []string{ssh, home}
	if os.Symlink(ssh, filepath.Join(outside, "keys")) == nil {
		entries = append(entries, filepath.Join(outside, "keys"))
	}
	for _, entry := range entries {
		t.Setenv(policy.TaskReadsEnv, policy.TaskReadsValue([]string{entry}))
		if out := call("Read", map[string]string{"file_path": key}); !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Errorf("Read list %s: %s", entry, out)
		}
	}
}

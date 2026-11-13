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

// Installed instructions are outside a peer's worktree. Reading them must
// not publish a person-only ask that stalls until its timeout.
func TestWorkerReadsAgentSkillsWithoutAsk(t *testing.T) {
	_, root := gateSetup(t, mode.Ask)
	shortAskWait(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, ".agent", "skills", "example", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"codex", "claude"} {
		for _, event := range []string{"PreToolUse", "PermissionRequest"} {
			for _, call := range []struct {
				tool  string
				input map[string]string
			}{
				{"Read", map[string]string{"file_path": file}},
				{"Bash", map[string]string{"command": "cat '" + filepath.ToSlash(file) + "'"}},
			} {
				// Codex exposes reads through Bash, not a Read hook.
				if agent == "codex" && call.tool == "Read" {
					continue
				}
				data, _ := json.Marshal(map[string]any{"session_id": "worker-skills", "cwd": root, "tool_name": call.tool, "tool_input": call.input})
				out, err := gateCommand(t, string(data), "gate", "--agent", agent, "--event", event)
				allowed := out == ""
				if event == "PermissionRequest" {
					allowed = strings.Contains(out, `"behavior":"allow"`)
				}
				if err != nil || !allowed {
					t.Errorf("%s %s %s: %s %v", agent, event, call.tool, out, err)
				}
			}
		}
	}
}

// A Windows home is often spelled with an 8.3 folder, as C:\Users\RUNNER~1 is
// on the CI machines. The quotes of a path that holds one only group literal
// text, so the read is judged like any other. A link stands in for the short
// name here: both are another spelling of the long folder.
func TestWorkerReadsAgentSkillsUnderAShortName(t *testing.T) {
	_, root := gateSetup(t, mode.Ask)
	shortAskWait(t)
	parent := t.TempDir()
	long := filepath.Join(parent, "runneradmin")
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(parent, "RUNNER~1")
	if err := os.Symlink(long, home); err != nil {
		t.Skip("cannot create a symlink here")
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	file := filepath.Join(home, ".agent", "skills", "example", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"codex", "claude"} {
		data, _ := json.Marshal(map[string]any{"session_id": "worker-short", "cwd": root, "tool_name": "Bash", "tool_input": map[string]string{"command": "cat '" + filepath.ToSlash(file) + "'"}})
		if out, err := gateCommand(t, string(data), "gate", "--agent", agent, "--event", "PreToolUse"); err != nil || out != "" {
			t.Errorf("%s: %s %v", agent, out, err)
		}
	}
}

// The gate applies the real width check to each resolved skill root: an
// agent's own folder keeps working, and one linked to a wide folder grants
// nothing.
func TestWorkerSkillRootsPassTheWidthCheck(t *testing.T) {
	_, root := gateSetup(t, mode.Ask)
	shortAskWait(t)
	allowed := func(t *testing.T, file string) bool {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"session_id": "worker-skills", "cwd": root, "tool_name": "Read", "tool_input": map[string]string{"file_path": file}})
		out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", "PermissionRequest")
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(out, `"behavior":"allow"`)
	}
	home := func(t *testing.T, files ...string) string {
		t.Helper()
		dir := t.TempDir()
		t.Setenv("HOME", dir)
		t.Setenv("USERPROFILE", dir)
		t.Setenv("CODEX_HOME", "")
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		for _, name := range files {
			putWorktreeFile(t, filepath.Join(dir, filepath.FromSlash(name)), "x\n")
		}
		return dir
	}
	link := func(t *testing.T, to, name string) {
		t.Helper()
		if err := os.Symlink(to, name); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	t.Run("own skill folders", func(t *testing.T) {
		dir := home(t, ".claude/skills/x/SKILL.md", ".codex/skills/x/SKILL.md", ".claude/plugins/cache/v/p/1/skills/x/SKILL.md")
		for _, name := range []string{".claude/skills/x/SKILL.md", ".codex/skills/x/SKILL.md", ".claude/plugins/cache/v/p/1/skills/x/SKILL.md"} {
			if !allowed(t, filepath.Join(dir, filepath.FromSlash(name))) {
				t.Errorf("%s asked", name)
			}
		}
	})
	t.Run("linked to its config folder", func(t *testing.T) {
		dir := home(t, ".claude/history.jsonl", ".claude/.credentials.json")
		link(t, filepath.Join(dir, ".claude"), filepath.Join(dir, ".claude", "skills"))
		for _, name := range []string{".claude/skills/history.jsonl", ".claude/skills/.credentials.json", ".claude/history.jsonl"} {
			if allowed(t, filepath.Join(dir, filepath.FromSlash(name))) {
				t.Errorf("%s allowed", name)
			}
		}
	})
	t.Run("linked to the home folder", func(t *testing.T) {
		dir := home(t, "notes/plan.md", ".ssh/id_rsa")
		if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
			t.Fatal(err)
		}
		link(t, dir, filepath.Join(dir, ".codex", "skills"))
		for _, name := range []string{".codex/skills/notes/plan.md", ".codex/skills/.ssh/id_rsa"} {
			if allowed(t, filepath.Join(dir, filepath.FromSlash(name))) {
				t.Errorf("%s allowed", name)
			}
		}
	})
}

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

// A worker in a linked worktree reads the main checkout as a Read entry; a
// worker in the main checkout itself, and a read tool that wrote .., get none.
func TestMainCheckoutReadsOnlyFromAnotherCheckout(t *testing.T) {
	main := pathutil.Resolved(t.TempDir())
	work := filepath.Join(main, ".worktrees", "w")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	listed := func() *policy.Action {
		return &policy.Action{Reads: []string{"/elsewhere/doc.md"}, WideRead: wideSkillRoot}
	}
	a := listed()
	mainCheckoutReads(a, main, work)
	if strings.Join(a.Reads, " ") != "/elsewhere/doc.md "+main {
		t.Fatalf("from a worktree: %q", a.Reads)
	}
	mainCheckoutReads(a, main, work)
	if len(a.Reads) != 2 {
		t.Fatalf("added twice: %q", a.Reads)
	}
	a = listed()
	mainCheckoutReads(a, main, main)
	if len(a.Reads) != 1 {
		t.Fatalf("in the main checkout: %q", a.Reads)
	}
	dotted := &policy.Action{}
	mainCheckoutReads(dotted, main, work)
	if dotted.Reads != nil {
		t.Fatalf("a read with ..: %q", dotted.Reads)
	}
}

package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

type fakeProcess struct {
	parent int
	name   string
}

func fakeTree(tree map[int]fakeProcess) func(int) (int, string) {
	return func(pid int) (int, string) {
		p, ok := tree[pid]
		if !ok {
			return 0, ""
		}
		return p.parent, p.name
	}
}

func TestAgentPIDSkipsShellsAndLaunchers(t *testing.T) {
	tree := map[int]fakeProcess{
		40: {30, "sh"},
		30: {25, "-zsh"},
		25: {20, "sandbox-exec"},
		20: {10, "claude"},
		10: {1, "login"},
	}
	if got := agentPID(40, fakeTree(tree)); got != 20 {
		t.Fatalf("agent PID %d, want 20", got)
	}
	// The direct parent is the agent when no shell stands between them.
	if got := agentPID(20, fakeTree(tree)); got != 20 {
		t.Fatalf("agent PID %d, want 20", got)
	}
	windows := map[int]fakeProcess{7: {6, "bash.exe"}, 6: {5, "node.exe"}}
	if got := agentPID(7, fakeTree(windows)); got != 6 {
		t.Fatalf("agent PID %d, want 6", got)
	}
}

func TestAgentPIDRefusesWithoutAnAgent(t *testing.T) {
	// An orphaned shell has no agent above it.
	if got := agentPID(40, fakeTree(map[int]fakeProcess{40: {1, "bash"}})); got != 0 {
		t.Fatalf("agent PID %d, want 0", got)
	}
	// A system that does not name processes cannot tell a shell from an agent.
	if got := agentPID(40, fakeTree(map[int]fakeProcess{40: {30, ""}})); got != 0 {
		t.Fatalf("agent PID %d, want 0", got)
	}
}

func TestFindTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	claude := filepath.Join(home, ".claude", "projects", "-work-app", "abc-123.jsonl")
	codex := filepath.Join(home, ".codex", "sessions", "2026", "11", "05", "rollout-2026-11-05T10-00-00-def-456.jsonl")
	for _, path := range []string{claude, codex} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := FindTranscript("claude", "abc-123", home); got != claude {
		t.Fatalf("claude %q", got)
	}
	if got := FindTranscript("codex", "def-456", home); got != codex {
		t.Fatalf("codex %q", got)
	}
	for _, id := range []string{"missing", "*", "../abc-123", ""} {
		if got := FindTranscript("claude", id, home); got != "" {
			t.Fatalf("id %q found %q", id, got)
		}
	}
}

func TestClaudeModeReadsLastRecordedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	body := `{"type":"user","permissionMode":"acceptEdits"}` + "\n" +
		`{"type":"assistant","message":{"model":"claude-sonnet-5-5"}}` + "\n" +
		`{"type":"user","permissionMode":"plan"}` + "\n" +
		`{"type":"user","message":{}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ClaudeMode(path); got != mode.Plan {
		t.Fatalf("mode %q", got)
	}
	if got := ClaudeMode(filepath.Join(t.TempDir(), "none.jsonl")); got != "" {
		t.Fatalf("missing transcript gave %q", got)
	}
}

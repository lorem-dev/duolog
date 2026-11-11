package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
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

func TestAgentAncestorFindsClaudeOrCodex(t *testing.T) {
	tests := []struct {
		name string
		tree map[int]fakeProcess
		want string
	}{
		{"claude above a pty wrapper", map[int]fakeProcess{40: {30, "script"}, 30: {25, "zsh"}, 25: {20, "claude"}, 20: {1, "launchd"}}, "claude"},
		{"codex above rex", map[int]fakeProcess{40: {30, "rex"}, 30: {20, "/opt/bin/codex"}, 20: {1, "init"}}, "codex"},
		{"windows names", map[int]fakeProcess{40: {30, "pwsh.exe"}, 30: {20, "Codex.exe"}}, "codex"},
		{"codex sandbox helper", map[int]fakeProcess{40: {30, "codex-linux-sandbox"}}, "codex"},
		{"the person's terminal", map[int]fakeProcess{40: {30, "zsh"}, 30: {20, "rex"}, 20: {1, "launchd"}}, ""},
		{"an unnamed process is passed over", map[int]fakeProcess{40: {30, ""}, 30: {20, "claude"}}, "claude"},
		{"a loop ends", map[int]fakeProcess{40: {40, "zsh"}}, ""},
		{"a name that only starts alike", map[int]fakeProcess{40: {30, "claudette"}, 30: {1, "init"}}, ""},
	}
	for _, tt := range tests {
		if got := agentAncestor(40, fakeTree(tt.tree)); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestAgentAncestorFromDescriptors walks the same trees as
// TestAgentAncestorFindsClaudeOrCodex, with the process names that the two
// agents' descriptors give.
func TestAgentAncestorFromDescriptors(t *testing.T) {
	names, helpers := []string{"claude", "codex", "codex-*"}, []string{"codex-linux-sandbox"}
	match := func(name string) string { return matchAgent(name, names, helpers) }
	tests := []struct {
		name string
		tree map[int]fakeProcess
		want string
	}{
		{"claude above a pty wrapper", map[int]fakeProcess{40: {30, "script"}, 30: {25, "zsh"}, 25: {20, "claude"}, 20: {1, "launchd"}}, "claude"},
		{"codex above rex", map[int]fakeProcess{40: {30, "rex"}, 30: {20, "/opt/bin/codex"}, 20: {1, "init"}}, "codex"},
		{"windows names", map[int]fakeProcess{40: {30, "pwsh.exe"}, 30: {20, "Codex.exe"}}, "codex"},
		{"codex target triple", map[int]fakeProcess{40: {30, "codex-x86_64-unknown-linux-musl"}}, "codex"},
		{"codex sandbox helper", map[int]fakeProcess{40: {30, "codex-linux-sandbox"}}, "codex"},
		{"the person's terminal", map[int]fakeProcess{40: {30, "zsh"}, 30: {20, "rex"}, 20: {1, "launchd"}}, ""},
		{"an unnamed process is passed over", map[int]fakeProcess{40: {30, ""}, 30: {20, "claude"}}, "claude"},
		{"a loop ends", map[int]fakeProcess{40: {40, "zsh"}}, ""},
		{"a name that only starts alike", map[int]fakeProcess{40: {30, "claudette"}, 30: {1, "init"}}, ""},
	}
	for _, tt := range tests {
		if got := agentAncestorBy(40, fakeTree(tt.tree), match); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// ParentAgent looks at the direct parent only: a shell between the agent and
// duolog, as in a model's Bash call, names no agent.
func TestParentAgentNamesOnlyTheDirectParent(t *testing.T) {
	names, helpers := []string{"claude", "codex", "codex-*"}, []string{"codex-linux-sandbox"}
	match := func(name string) string { return matchAgent(name, names, helpers) }
	tests := []struct {
		name string
		tree map[int]fakeProcess
		want string
	}{
		{"claude is the parent", map[int]fakeProcess{40: {30, "claude"}, 30: {1, "launchd"}}, "claude"},
		{"a shell under claude", map[int]fakeProcess{40: {30, "zsh"}, 30: {20, "claude"}}, ""},
		{"codex is the parent", map[int]fakeProcess{40: {30, "/opt/bin/codex"}}, "codex"},
		{"an unnamed parent", map[int]fakeProcess{40: {30, ""}, 30: {20, "claude"}}, ""},
	}
	for _, tt := range tests {
		if got := parentAgentBy(40, fakeTree(tt.tree), match); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
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

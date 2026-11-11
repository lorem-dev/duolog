package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

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
		if got := parentAgentBy(40, fakeTree(tt.tree), noArgs, noLinks, match, nil); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func noArgs(int) []string { return nil }

func noLinks(string) string { return "" }

// An npm install of Claude Code runs as node with the CLI script as its
// first argument: the script itself (Windows' npm shim), or the bin link
// that resolves to it. ParentAgent names it only then: no option of any kind
// may come before the script, and the script's path must end in a script of
// the descriptors. A file merely named claude names nothing.
func TestParentAgentKnowsClaudeUnderNode(t *testing.T) {
	names, helpers := []string{"claude", "codex", "codex-*"}, []string{"codex-linux-sandbox"}
	scripts := map[string]string{"@anthropic-ai/claude-code/cli.js": "claude"}
	match := func(name string) string { return matchAgent(name, names, helpers) }
	tree := fakeTree(map[int]fakeProcess{40: {30, "node"}, 30: {1, "launchd"}})
	const cli = "/usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js"
	// resolve stands for the file system: the npm bin link resolves to the
	// CLI script; a relative link must never be looked up.
	resolve := func(path string) string {
		switch path {
		case "/usr/local/bin/claude", "bin/claude", `C:\npm\claude`:
			return cli
		}
		return ""
	}
	// The bin link is an absolute path of the system that runs the test: a
	// path from / is not absolute on Windows, one from a drive only there.
	binLink := []string{"node", "/usr/local/bin/claude", "--resume"}
	if runtime.GOOS == "windows" {
		binLink = []string{"node", `C:\npm\claude`, "--resume"}
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"the CLI script", []string{"/usr/bin/node", cli}, "claude"},
		{"Windows' shim", []string{`C:\Program Files\nodejs\node.exe`, `C:\Users\u\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\cli.js`}, "claude"},
		{"the bin link", binLink, "claude"},
		{"a relative bin link", []string{"node", "bin/claude"}, ""},
		{"a script named claude elsewhere", []string{"node", "/opt/tools/claude"}, ""},
		{"a node option first", []string{"node", "--no-warnings", cli}, ""},
		{"clustered short options", []string{"node", "-pe", "'x'//x/claude", cli}, ""},
		{"evaluated code", []string{"node", "-e", cli}, ""},
		{"a required module", []string{"node", "-r", "x", cli}, ""},
		{"a required module, long", []string{"node", "--require", "x", cli}, ""},
		{"a title", []string{"node", "--title", cli, "other.js"}, ""},
		{"a title value", []string{"node", "--title", "v", cli}, ""},
		{"a package script", []string{"node", "--run", "claude"}, ""},
		{"another node program", []string{"node", "/srv/app/server.js"}, ""},
		{"a script only named alike", []string{"node", "/tmp/not-anthropic-ai/claude-code/cli.js.bak"}, ""},
		{"a later argument", []string{"node", "/srv/app/server.js", cli}, ""},
		{"an empty first argument", []string{"node", "", cli}, ""},
		{"no arguments read", nil, ""},
	}
	for _, tt := range tests {
		args := func(pid int) []string {
			if pid != 40 {
				t.Fatalf("%s: read the arguments of %d", tt.name, pid)
			}
			return tt.args
		}
		if got := parentAgentBy(40, tree, args, resolve, match, scripts); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	// A shell between node and duolog still names none.
	shell := fakeTree(map[int]fakeProcess{40: {30, "zsh"}, 30: {20, "node"}})
	if got := parentAgentBy(40, shell, func(int) []string { return []string{"node", cli} }, resolve, match, scripts); got != "" {
		t.Errorf("a shell under node: %q", got)
	}
}

// processArgs reads a process's arguments where the system gives them.
func TestProcessArgsReadsThisProcess(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("arguments are read on Linux, macOS and Windows only")
	}
	got := processArgs(os.Getpid())
	if len(got) != len(os.Args) || filepath.Base(got[0]) != filepath.Base(os.Args[0]) || !slices.Equal(got[1:], os.Args[1:]) {
		t.Fatalf("args %q, want %q", got, os.Args)
	}
	if got := processArgs(1 << 30); got != nil {
		t.Fatalf("a missing process gave %q", got)
	}
}

// processArgs reads another process's arguments whole, whatever the length
// of its executable's path (the padding after it on macOS) and with empty
// arguments, an empty argv[0] among them.
func TestProcessArgsReadsAChild(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("a shell child is started on Linux and macOS only")
	}
	dir := t.TempDir()
	for n := 1; n <= 9; n++ {
		for _, argv0 := range []string{"sh", ""} {
			link := filepath.Join(dir, strings.Repeat("s", n))
			if _, err := os.Lstat(link); err != nil {
				if err := os.Symlink("/bin/sh", link); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{argv0, "-c", "sleep 5; :", "x", "", "b"}
			cmd := exec.Cmd{Path: link, Args: args}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// Right after the fork the child may not have run exec yet, and
			// Linux shows no arguments until it has.
			var got []string
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if got = processArgs(cmd.Process.Pid); slices.Equal(got, args) {
					break
				}
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if !slices.Equal(got, args) {
				t.Errorf("path of %d, argv0 %q: args %q, want %q", len(link), argv0, got, args)
			}
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

package codex

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/install"
)

const hookContractWarning = "Changing this text makes every person approve the hooks again in /hooks. Add a CHANGES.md entry saying so."

var update = flag.Bool("update", false, "rewrite the golden files in testdata/hooks")

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "hooks", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it). %s", err, hookContractWarning)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from its golden. %s\n got: %q\nwant: %q", name, hookContractWarning, got, want)
	}
}

// The hooks document holds both Codex shell forms in every file, so the
// golden is the same on every OS. Kills mutations to any event, command,
// commandWindows, timeout, or the absence of a matcher.
func TestHooksJSONGolden(t *testing.T) {
	data, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "codex-all-hooks", data)
}

// Each golden records the POSIX and the Windows command, on every OS.
func TestCodexGateCommandsGolden(t *testing.T) {
	for _, event := range []string{"PreToolUse", "PermissionRequest"} {
		t.Run(event, func(t *testing.T) {
			posix, windows := CodexGateCommands(event)
			checkGolden(t, "codex-"+event+"-commands", []byte("posix:\n"+posix+"\nwindows:\n"+windows+"\n"))
		})
	}
}

// Merged into an empty file, the hooks are the document of the golden; a
// second merge changes nothing; a foreign handler is kept.
func TestInstallerHooks(t *testing.T) {
	var i Installer
	merged, notes, err := i.Hooks(nil, install.GateTimeout)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "/hooks") {
		t.Fatalf("Hooks into no file: %v, %q", err, notes)
	}
	want, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(merged, want) {
		t.Errorf("hooks merged into no file differ from HooksJSON\n got: %s\nwant: %s", merged, want)
	}
	again, _, err := i.Hooks(merged, install.GateTimeout)
	if err != nil || !bytes.Equal(again, merged) {
		t.Errorf("a second merge changed the file (%v)\n%s", err, again)
	}
	if i.GateChanged(merged, again) {
		t.Error("an unchanged gate reads as changed")
	}
	foreign := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other"}]}]}}`)
	kept, _, err := i.Hooks(foreign, install.GateTimeout)
	if err != nil || !bytes.Contains(kept, []byte(`"command": "other"`)) {
		t.Errorf("a foreign handler was lost (%v)\n%s", err, kept)
	}
	removed, notes, err := i.Hooks(kept, 0)
	if err != nil || len(notes) != 0 || bytes.Contains(removed, []byte("duolog")) || !bytes.Contains(removed, []byte(`"command": "other"`)) {
		t.Errorf("a timeout of zero must remove only duolog's hooks (%v, %q)\n%s", err, notes, removed)
	}
	if !i.GateChanged(kept, removed) {
		t.Error("removing the gate reads as unchanged")
	}
}

func TestInstallerPaths(t *testing.T) {
	var i Installer
	base, home := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", "")
	if got, want := i.HookFile("project", base, home), filepath.Join(base, ".codex", "hooks.json"); got != want {
		t.Errorf("HookFile project = %q, want %q", got, want)
	}
	if got, want := i.HookFile("user", home, home), filepath.Join(home, ".codex", "hooks.json"); got != want {
		t.Errorf("HookFile user = %q, want %q", got, want)
	}
	custom := t.TempDir()
	t.Setenv("CODEX_HOME", custom)
	if got, want := i.HookFile("user", home, home), filepath.Join(custom, "hooks.json"); got != want {
		t.Errorf("HookFile user with CODEX_HOME = %q, want %q", got, want)
	}
	if got, want := i.SkillDir("project", base, home), filepath.Join(base, ".agents", "skills"); got != want {
		t.Errorf("SkillDir = %q, want %q", got, want)
	}
	if current, all := i.InstructionFiles(base); current != filepath.Join(base, "AGENTS.md") || len(all) != 1 {
		t.Errorf("InstructionFiles = %q, %q", current, all)
	}
}

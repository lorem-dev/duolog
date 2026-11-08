package claude

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/install"
)

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
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from its golden\n got: %q\nwant: %q", name, got, want)
	}
}

func TestHooksJSONGolden(t *testing.T) {
	data, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "claude-all-hooks", data)
}

// Merged into an empty file, the hooks are the document of the golden; a
// second merge changes nothing; a foreign handler is kept.
func TestInstallerHooks(t *testing.T) {
	var i Installer
	merged, notes, err := i.Hooks(nil, install.GateTimeout)
	if err != nil || len(notes) != 0 {
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
	foreign := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other"}]}]}}`)
	kept, _, err := i.Hooks(foreign, install.GateTimeout)
	if err != nil || !bytes.Contains(kept, []byte(`"command": "other"`)) {
		t.Errorf("a foreign handler was lost (%v)\n%s", err, kept)
	}
	removed, _, err := i.Hooks(kept, 0)
	if err != nil || bytes.Contains(removed, []byte("duolog")) || !bytes.Contains(removed, []byte(`"command": "other"`)) {
		t.Errorf("a timeout of zero must remove only duolog's hooks (%v)\n%s", err, removed)
	}
}

func TestInstallerPaths(t *testing.T) {
	var i Installer
	base, home := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got, want := i.HookFile("project", base, home), filepath.Join(base, ".claude", "settings.json"); got != want {
		t.Errorf("HookFile project = %q, want %q", got, want)
	}
	if got, want := i.HookFile("user", home, home), filepath.Join(home, ".claude", "settings.json"); got != want {
		t.Errorf("HookFile user = %q, want %q", got, want)
	}
	if got, want := i.SkillDir("project", base, home), filepath.Join(base, ".claude", "skills"); got != want {
		t.Errorf("SkillDir project = %q, want %q", got, want)
	}
	if got, want := i.SkillDir("user", home, home), filepath.Join(home, ".claude", "skills"); got != want {
		t.Errorf("SkillDir user = %q, want %q", got, want)
	}
	if current, all := i.InstructionFiles(base); current != filepath.Join(base, "CLAUDE.md") || len(all) != 2 {
		t.Errorf("InstructionFiles = %q, %q", current, all)
	}
	if err := os.MkdirAll(filepath.Join(base, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".claude", "CLAUDE.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if current, _ := i.InstructionFiles(base); current != filepath.Join(base, ".claude", "CLAUDE.md") {
		t.Errorf("InstructionFiles with .claude/CLAUDE.md = %q", current)
	}
}

package trust

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if e := os.MkdirAll(filepath.Join(home, ".codex"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestFolderTrustReportedMissing(t *testing.T) {
	root := t.TempDir()
	fixture(t, fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", t.TempDir()))
	ok, e := Folder("codex", root)
	if e != nil || ok {
		t.Fatal(ok, e)
	}
	fixture(t, fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", root))
	ok, e = Folder("codex", root)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
}
func TestHookTrustMatchesProject(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, ".codex", "hooks.json") + ":PreToolUse:0:0"
	fixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"invented-hash\"\n", key))
	writeHook(t, root, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`)
	s, e := Hooks("codex", root)
	if e != nil || !s.Trusted {
		t.Fatal(s, e)
	}
	s, e = Hooks("codex", t.TempDir())
	if e != nil || s.Trusted {
		t.Fatal(s, e)
	}
}
func TestClaudeSkipsTrust(t *testing.T) {
	fixture(t, "invalid TOML [")
	root := t.TempDir()
	if ok, e := Folder("claude", root); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if s, e := Hooks("claude", root); e != nil || !s.Trusted {
		t.Fatal(s, e)
	}
	if _, e := Hooks("codex", root); e == nil {
		t.Fatal("malformed configuration accepted")
	}
}

func writeHook(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestHookTrustRequiresCurrentGateEntry(t *testing.T) {
	for _, tc := range []struct {
		name, body, entry string
		trusted           bool
	}{
		{"missing file", "", "0:0", false},
		{"deleted gate", `{"hooks":{"PreToolUse":[]}}`, "0:0", false},
		{"changed command", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo duolog gate"}]}]}}`, "0:0", false},
		{"different event", `{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`, "0:0", false},
		{"wrong entry approved", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo hello"},{"type":"command","command":"duolog gate"}]}]}}`, "0:0", false},
		{"second entry approved", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"echo hello"},{"type":"command","command":"duolog gate"}]}]}}`, "0:1", true},
		{"different group approved", `{"hooks":{"PreToolUse":[{"hooks":[]},{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`, "1:0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			key := filepath.Join(root, ".codex", "hooks.json") + ":PreToolUse:" + tc.entry
			fixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"reviewed-hash\"\n", key))
			if tc.body != "" {
				writeHook(t, root, tc.body)
			}
			got, err := Hooks("codex", root)
			if err != nil || got.Trusted != tc.trusted {
				t.Fatalf("trusted=%v want %v, err=%v", got.Trusted, tc.trusted, err)
			}
		})
	}
}

func TestTrustWindowsOverrideOnly(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, ".codex", "hooks.json") + ":PreToolUse:0:0"
	fixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture\"\n", key))
	writeHook(t, root, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","commandWindows":"powershell -NoProfile -NonInteractive -Command duolog gate --agent codex"}]}]}}`)
	state, err := Hooks("codex", root)
	if err != nil || !state.Installed || !state.Trusted {
		t.Fatal(state, err)
	}
}

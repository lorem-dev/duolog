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
	if e := os.MkdirAll(filepath.Join(home, ".codex"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestFolderTrustReportedMissing(t *testing.T) {
	root := "/invented/project"
	fixture(t, `[projects."/invented/other"]`+"\ntrust_level = \"trusted\"\n")
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
	root := "/invented/project"
	key := filepath.Join(root, ".codex", "hooks.json") + ":PreToolUse:0:0"
	fixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"invented-hash\"\n", key))
	s, e := Hooks("codex", root)
	if e != nil || !s.Trusted {
		t.Fatal(s, e)
	}
	s, e = Hooks("codex", "/invented/other")
	if e != nil || s.Trusted {
		t.Fatal(s, e)
	}
}
func TestClaudeSkipsTrust(t *testing.T) {
	fixture(t, "invalid TOML [")
	if ok, e := Folder("claude", "/any"); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if s, e := Hooks("claude", "/any"); e != nil || !s.Trusted {
		t.Fatal(s, e)
	}
	if _, e := Hooks("codex", "/any"); e == nil {
		t.Fatal("malformed configuration accepted")
	}
}

package trust

import (
	"fmt"
	"github.com/lorem-dev/duolog/internal/install"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedHooksTrustedAtBothScopesAndCodexHome(t *testing.T) {
	for _, scope := range []string{"project", "user", "codex-home"} {
		t.Run(scope, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("USERPROFILE", home)
			t.Setenv("CODEX_HOME", "")
			dir := filepath.Join(root, ".codex")
			if scope == "user" {
				dir = filepath.Join(home, ".codex")
			}
			configDir := filepath.Join(home, ".codex")
			if scope == "codex-home" {
				configDir = t.TempDir()
				dir = configDir
				t.Setenv("CODEX_HOME", configDir)
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			data, err := install.HooksJSON("codex", "linux", 150)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "hooks.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(configDir, 0755); err != nil {
				t.Fatal(err)
			}
			text := fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture\"\n[projects.%q]\ntrust_level = \"trusted\"\n", path+":PreToolUse:0:0", root)
			if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := Hooks("codex", root)
			if err != nil || !got.Trusted {
				t.Fatal(got, err)
			}
			if ok, err := Folder("codex", root); err != nil || !ok {
				t.Fatal(ok, err)
			}
		})
	}
}

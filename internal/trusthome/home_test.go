package trusthome

import (
	"path/filepath"
	"testing"
)

func TestConfigOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if Codex(home) != filepath.Join(home, ".codex") || Claude(home) != filepath.Join(home, ".claude") {
		t.Fatal("defaults")
	}
	codex, claude := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	if Codex(home) != codex || Claude(home) != claude {
		t.Fatal("overrides")
	}
}

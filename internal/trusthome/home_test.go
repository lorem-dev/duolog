package trusthome

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

// HomeIn reads the home folder from the environment it is given: HOME, or
// USERPROFILE on Windows, never duolog's own.
func TestHomeInReadsTheEnvironment(t *testing.T) {
	name, other := "HOME", "USERPROFILE"
	if runtime.GOOS == "windows" {
		name, other = other, name
	}
	if got, err := HomeIn([]string{name + "=/w/home", other + "=/elsewhere"}); err != nil || got != "/w/home" {
		t.Fatalf("HomeIn = %q, %v", got, err)
	}
	if _, err := HomeIn([]string{other + "=/elsewhere", "PATH=/bin"}); !errors.Is(err, ErrNoHome) {
		t.Fatalf("without %s: %v, want ErrNoHome", name, err)
	}
	if _, err := HomeIn([]string{name + "="}); !errors.Is(err, ErrNoHome) {
		t.Fatalf("an empty %s: %v, want ErrNoHome", name, err)
	}
}

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

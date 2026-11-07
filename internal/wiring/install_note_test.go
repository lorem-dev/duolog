package wiring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/hook"
	"github.com/lorem-dev/duolog/internal/app/install"
)

// installNoteProject installs duolog for both agents in a new project with a
// temporary home, with Codex's skills where the config puts them.
func installNoteProject(t *testing.T, config string) string {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(key, home)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := t.TempDir()
	if config != "" {
		if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, ok := projectInstall(root)
	if !ok {
		t.Fatal("the config does not load")
	}
	if _, err := d.Run(install.Options{Scope: "project", Root: root, Home: home}); err != nil {
		t.Fatal(err)
	}
	return root
}

// The SessionStart note that HookEnv wires names the fix for a stale project
// install, and reads agents.codex.skills_dir as install does.
func TestHookEnvInstallNote(t *testing.T) {
	root := installNoteProject(t, "version: 1\nagents:\n  codex:\n    skills_dir: .codex/skills\n")
	env := HookEnv(hook.Env{Agent: "claude", Root: root})
	if env.InstallNote == nil {
		t.Fatal("HookEnv sets no InstallNote")
	}
	if note := env.InstallNote(root); note != "" {
		t.Fatalf("a current install with skills_dir has a note: %q", note)
	}
	if err := os.Remove(filepath.Join(root, ".codex", "skills", "duolog", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if note := env.InstallNote(root); !strings.Contains(note, "run duolog install --scope project") {
		t.Fatalf("note %q", note)
	}
}

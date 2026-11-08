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
	// An unchanged copy of an earlier install in the configured folder is
	// one install would remove.
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".agents", "skills", "duolog", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".codex", "skills", "duolog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if note := env.InstallNote(root); !strings.Contains(note, "run duolog install --scope project") {
		t.Fatalf("note %q", note)
	}
}

// The SessionStart refresh that HookEnv wires replaces a user skill an older
// duolog wrote, under the home of the process.
func TestHookEnvRefreshSkills(t *testing.T) {
	root := installNoteProject(t, "")
	env := HookEnv(hook.Env{Agent: "claude", Root: root})
	if env.RefreshSkills == nil {
		t.Fatal("HookEnv sets no RefreshSkills")
	}
	if line := env.RefreshSkills(root); line != "" {
		t.Fatalf("current skills refreshed: %q", line)
	}
	path := filepath.Join(os.Getenv("HOME"), ".claude", "skills", "duolog", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// An older release names its own version in every place.
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), install.Version(), "1.0.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	if line := env.RefreshSkills(root); !strings.Contains(line, install.Version()) {
		t.Fatalf("line %q", line)
	}
	if have, _ := os.ReadFile(path); string(have) != string(data) {
		t.Fatal("the older skill was not replaced")
	}
}

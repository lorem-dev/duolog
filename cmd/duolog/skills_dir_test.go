package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillsDirProject is a project whose config puts Codex's skills in
// .codex/skills, with one Claude skill to sync. HOME and the config home are
// temporary, so nothing reaches the real ones.
func skillsDirProject(t *testing.T) string {
	t.Helper()
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	for path, data := range map[string]string{
		".duolog/config.yaml":           "version: 1\nagents:\n  codex:\n    skills_dir: .codex/skills\n",
		".claude/skills/probe/SKILL.md": "---\nname: probe\ndescription: d\n---\nbody\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSyncCommandHonoursSkillsDir(t *testing.T) {
	root := skillsDirProject(t)
	out, _, err := runSync(t, "--from", "claude", "--dry-run")
	if err != nil || !strings.Contains(out, ".codex/skills/probe/SKILL.md") || strings.Contains(out, ".agents") {
		t.Fatalf("dry run: %v %q", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote")
	}
	if out, _, err = runSync(t, "--from", "claude"); err != nil || !strings.Contains(out, ".codex/skills/probe/SKILL.md") {
		t.Fatalf("%v %q", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Fatal(".agents was created")
	}
	if _, _, err := runSync(t, "--check"); err != nil {
		t.Fatal(err)
	}
}

func TestInstallCommandHonoursSkillsDir(t *testing.T) {
	root := skillsDirProject(t)
	for _, verb := range []string{"install", "uninstall"} {
		cmd := newRootCommand(testDeps(t))
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{verb, "--scope", "project", "--agent", "codex"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(filepath.Join(root, ".codex/skills/duolog/SKILL.md"))
		if (verb == "install") != (err == nil) {
			t.Errorf("after %s: stat err = %v", verb, err)
		}
		if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
			t.Errorf("after %s: .agents exists", verb)
		}
	}
}

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCommandProject(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Chdir(root)
	cmd := newRootCommand(testDeps(t))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"install", "--scope", "project", "--agent", "codex"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/hooks") {
		t.Fatalf("output: %q", out.String())
	}
	// The skills are the one thing a Codex install writes at user scope.
	if entries, _ := os.ReadDir(home); len(entries) != 1 || entries[0].Name() != ".agents" {
		t.Fatalf("home changed: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "duolog", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	cmd = newRootCommand(testDeps(t))
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"uninstall", "--scope", "project", "--agent", "codex"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "duolog")); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the user skill: %v", err)
	}
	if !strings.Contains(out.String(), "duolog skills removed for all projects") {
		t.Fatalf("output: %q", out.String())
	}
}

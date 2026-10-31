package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSync(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newRootCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"sync"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestSyncCommandSkillsAndCheck(t *testing.T) {
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	path := filepath.Join(root, ".claude/skills/probe/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: probe\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runSync(t, "--check", "--from", "claude"); err == nil {
		t.Fatal("check passed with drift")
	}
	if _, err := os.Stat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Fatal("check wrote")
	}
	out, _, err := runSync(t, "--from", "claude")
	if err != nil || !strings.Contains(out, ".agents/skills/probe/SKILL.md") {
		t.Fatalf("%v %q", err, out)
	}
	if _, _, err := runSync(t, "--check"); err != nil {
		t.Fatal(err)
	}
	ignore, _ := os.ReadFile(filepath.Join(root, ".duolog/.gitignore"))
	if !strings.Contains(string(ignore), "!sync.lock.json") {
		t.Fatalf("%q", ignore)
	}
}

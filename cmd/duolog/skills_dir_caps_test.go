package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func capsProject(t *testing.T, config string) string {
	t.Helper()
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: `{"installed":[]}`}}})
	t.Setenv("PATH", filepath.Dir(claude)+string(os.PathListSeparator)+filepath.Dir(codex))
	for rel, data := range map[string]string{
		".duolog/config.yaml":         config,
		"tools/skills/mine/SKILL.md":  "---\nname: mine\ndescription: d\n---\n",
		".agents/skills/old/SKILL.md": "---\nname: old\ndescription: d\n---\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// In a linked worktree, caps lists the skills of the worktree: its files, not
// the main checkout's, are the ones the agent loads there.
func TestCapsCommandScansTheWorkRoot(t *testing.T) {
	// capsProject narrows PATH to the fake agents, so find git first.
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	main := capsProject(t, "version: 1\n")
	work := filepath.Join(t.TempDir(), "work")
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "start"},
		{"worktree", "add", "-q", "--detach", work},
	} {
		cmd := exec.Command(gitBin, append([]string{"-C", main}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	path := filepath.Join(work, ".agents/skills/workonly/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: workonly\ndescription: d\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// duolog finds the main checkout with git, after the fake agents.
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+filepath.Dir(gitBin))
	t.Chdir(work)
	out, err := executeBusCommand(t, "caps")
	if err != nil || !strings.Contains(out, "workonly") || strings.Contains(out, "old") {
		t.Fatalf("%v %q", err, out)
	}
}

func TestCapsCommandReadsConfiguredSkillsDir(t *testing.T) {
	capsProject(t, "version: 1\nagents:\n  codex:\n    skills_dir: tools/skills\n")
	out, err := executeBusCommand(t, "caps")
	if err != nil || !strings.Contains(out, "mine") || !strings.Contains(out, "old") {
		t.Fatalf("%v %q", err, out)
	}
}

// A skills_dir that does not pass the checks must not stop a command that
// only lists: caps falls back to the default folders.
func TestCapsCommandFallsBackWhenConfigDoesNotLoad(t *testing.T) {
	capsProject(t, "version: 1\nagents:\n  codex:\n    skills_dir: ../outside\n")
	out, err := executeBusCommand(t, "caps")
	if err != nil || !strings.Contains(out, "old") || strings.Contains(out, "mine") {
		t.Fatalf("%v %q", err, out)
	}
}

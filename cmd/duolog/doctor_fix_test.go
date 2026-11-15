package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// fixGit runs git in dir without the caller's Git variables or config.
func fixGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=duolog", "-c", "user.email=duolog@example.invalid",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// Doctor names --fix for a linked worktree the sweep can give trust, writes
// nothing without it, and with it gives the worktree the main checkout's
// trust and says so.
func TestDoctorFixGivesLinkedWorktreeTrust(t *testing.T) {
	isolatedRouteConfig(t)
	base := t.TempDir()
	main, work := filepath.Join(base, "main"), filepath.Join(base, "work")
	hooks, err := launchtest.HooksJSON("codex", 30)
	if err != nil {
		t.Fatal(err)
	}
	putWorktreeFile(t, filepath.Join(main, ".codex", "hooks.json"), string(hooks))
	fixGit(t, main, "init", "-q")
	fixGit(t, main, "add", ".codex/hooks.json")
	fixGit(t, main, "commit", "-q", "-m", "hooks")
	fixGit(t, main, "worktree", "add", "-q", "--detach", work)
	putWorktreeFile(t, filepath.Join(main, ".duolog", "config.yaml"), "agents:\n  codex:\n    enabled: true\n")
	config := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[hooks.state.%q]\ntrusted_hash = %q\n",
		main, filepath.Join(main, ".codex", "hooks.json")+":PreToolUse:0:0", fixtureCodexApproval(t, 30))
	path := filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")
	putWorktreeFile(t, path, config)
	t.Chdir(work)
	testbin.Install(t, "codex", testbin.Scenario{Stdout: "{}"})
	testbin.Install(t, "claude", testbin.Scenario{})
	fakeRexFirst(t)

	out, err := executeBusCommand(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "folder trusted=false") || !strings.Contains(out, "duolog doctor --fix") {
		t.Fatalf("doctor does not name --fix:\n%s", out)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != config {
		t.Fatalf("doctor without --fix wrote: %q %v", after, err)
	}

	out, err = executeBusCommand(t, "doctor", "--fix")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fixed: Codex now trusts the linked worktree ") || !strings.Contains(out, string(filepath.Separator)+"work, as it trusts ") ||
		strings.Contains(out, "folder trusted=false") {
		t.Fatalf("doctor --fix:\n%s", out)
	}
	out, err = executeBusCommand(t, "doctor", "--fix")
	if err != nil || !strings.Contains(out, "info: worktree trust: nothing to fix") || strings.Contains(out, "fixed:") {
		t.Fatalf("second doctor --fix: %v\n%s", err, out)
	}
}

package config

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// linkedWorktree makes a main checkout with one commit and a linked worktree
// of it, and returns both. The main checkout is the root of the state.
func linkedWorktree(t *testing.T) (main, work string) {
	t.Helper()
	main = isolatedRoot(t)
	work = filepath.Join(t.TempDir(), "work")
	for _, args := range [][]string{
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "start"},
		{"worktree", "add", "-q", "--detach", work},
	} {
		cmd := exec.Command("git", append([]string{"-C", main}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return main, work
}

// In the main checkout itself there is no other checkout to read from.
func TestMainCheckoutIsEmptyInTheMainCheckout(t *testing.T) {
	main, work := linkedWorktree(t)
	if got := mainCheckout(main); got != "" {
		t.Errorf("mainCheckout(main) = %q, want none", got)
	}
	if got := mainCheckout(work); got == "" {
		t.Error("mainCheckout(worktree) found nothing")
	}
}

// config.local.yaml is not in Git, so a linked worktree has none. Load reads
// the one of the main checkout, and so does the key for the worktree, with the
// same precedence: the worktree's own config.yaml, then the local file.
func TestSkillsDirWorktreeFallsBackToMainLocalConfig(t *testing.T) {
	main, work := linkedWorktree(t)
	writeConfig(t, localConfigPath(main), "agents: {codex: {skills_dir: local/skills}}\n")
	dirs, err := ProjectSkillsDirs(work, BuiltinAgents())
	if err != nil || dirs["codex"] != "local/skills" {
		t.Fatalf("ProjectSkillsDirs = %v, %v; want local/skills from the main checkout", dirs, err)
	}
	// The worktree's own project config is read from the worktree, and the
	// local file of the main checkout still wins over it, as in Load.
	writeConfig(t, projectConfigPath(work), "agents: {codex: {skills_dir: shared/skills}}\n")
	dirs, err = ProjectSkillsDirs(work, BuiltinAgents())
	if err != nil || dirs["codex"] != "local/skills" {
		t.Fatalf("ProjectSkillsDirs = %v, %v; want the local file to win", dirs, err)
	}
	// A local file in the worktree replaces the one of the main checkout.
	writeConfig(t, localConfigPath(work), "agents: {codex: {skills_dir: own/skills}}\n")
	dirs, err = ProjectSkillsDirs(work, BuiltinAgents())
	if err != nil || dirs["codex"] != "own/skills" {
		t.Fatalf("ProjectSkillsDirs = %v, %v; want own/skills", dirs, err)
	}
}

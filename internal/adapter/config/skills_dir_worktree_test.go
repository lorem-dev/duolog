package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// Git variables of the caller, as a Git hook exports them, do not say which
// repository a folder is in.
func TestMainCheckoutIgnoresGitVariablesOfTheCaller(t *testing.T) {
	main, work := linkedWorktree(t)
	other, _ := linkedWorktree(t)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	got := mainCheckout(work)
	a, errA := os.Stat(got)
	b, errB := os.Stat(main)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Fatalf("mainCheckout(worktree) = %q, want %q", got, main)
	}
}

// CleanGitEnv takes out every Git variable that names a repository.
func TestCleanGitEnvDropsGitVariablesButTheCeiling(t *testing.T) {
	got := CleanGitEnv([]string{"PATH=/bin", "GIT_DIR=/x", "git_work_tree=/y", "HOME=/h", "GIT_CEILING_DIRECTORIES=/"})
	if want := []string{"PATH=/bin", "HOME=/h", "GIT_CEILING_DIRECTORIES=/"}; !slices.Equal(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
}

// The ceiling is the person's limit on how far up Git looks, so it stays.
func TestMainCheckoutKeepsTheCeiling(t *testing.T) {
	main, _ := linkedWorktree(t)
	sub := filepath.Join(main, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if mainCheckout(sub) == "" {
		t.Fatal("a folder of the main checkout found nothing without a ceiling")
	}
	real, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", real)
	if got := mainCheckout(sub); got != "" {
		t.Fatalf("mainCheckout past the ceiling = %q", got)
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

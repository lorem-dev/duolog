package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "protocol.file.allow=always", "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = configfile.CleanGitEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func same(t *testing.T, got, want string) {
	t.Helper()
	a, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Fatalf("state root %s, want %s", got, want)
	}
}

func TestStateRootOfASubmoduleIsTheSubmodule(t *testing.T) {
	base := t.TempDir()
	child, parent := filepath.Join(base, "child"), filepath.Join(base, "parent")
	for _, dir := range []string{child, parent} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "init", "-q")
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "x")
	}
	git(t, parent, "submodule", "add", "-q", child, "sm")
	same(t, StateRoot(filepath.Join(parent, "sm")), filepath.Join(parent, "sm"))
	same(t, StateRoot(parent), parent)
}

func TestStateRootOfALinkedWorktreeOfASubmoduleIsTheSubmodule(t *testing.T) {
	base := t.TempDir()
	child, parent := filepath.Join(base, "child"), filepath.Join(base, "parent")
	for _, dir := range []string{child, parent} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "init", "-q")
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "x")
	}
	git(t, parent, "submodule", "add", "-q", child, "sm")
	linked := filepath.Join(parent, "sm", ".worktrees", "w0")
	git(t, filepath.Join(parent, "sm"), "worktree", "add", "-q", linked)
	same(t, StateRoot(linked), filepath.Join(parent, "sm"))
}

// A submodule and its linked worktrees name the checkout that includes the
// submodule; a repository that is no submodule names none.
func TestSuperprojectOfASubmodule(t *testing.T) {
	base := t.TempDir()
	child, parent := filepath.Join(base, "child"), filepath.Join(base, "parent")
	for _, dir := range []string{child, parent} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "init", "-q")
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "x")
	}
	git(t, parent, "submodule", "add", "-q", child, "sm")
	linked := filepath.Join(parent, "sm", ".worktrees", "w0")
	git(t, filepath.Join(parent, "sm"), "worktree", "add", "-q", linked)
	for _, dir := range []string{filepath.Join(parent, "sm"), linked} {
		got := Superproject(dir)
		if got == "" {
			t.Fatalf("%s names no superproject", dir)
		}
		same(t, got, parent)
	}
	for _, dir := range []string{parent, child, base} {
		if got := Superproject(dir); got != "" {
			t.Fatalf("%s names superproject %s", dir, got)
		}
	}
}

// gitCalls puts a git on PATH that records each call and runs the real one,
// and returns a function that reads and clears the calls made so far.
func gitCalls(t *testing.T) func() []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the counting git is a shell script")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	bin, log := filepath.Join(dir, "bin"), filepath.Join(dir, "calls")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$GITCOUNT_LOG\"\nexec \"$GITCOUNT_REAL\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITCOUNT_LOG", log)
	t.Setenv("GITCOUNT_REAL", real)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		data, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(log); err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}
}

// failGit leaves no git on PATH, so every Git command fails to start.
func failGit(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// A main checkout is its own state root by its .git folder, and a project no
// folder above holds a .git entry is no submodule: neither runs Git.
func TestLookupsOfAPlainCheckoutRunNoGit(t *testing.T) {
	main := filepath.Join(t.TempDir(), "p")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q")
	git(t, main, "commit", "-q", "--allow-empty", "-m", "x")
	calls := gitCalls(t)
	got, err := CheckedStateRoot(main)
	if err != nil || got != main {
		t.Fatal(got, err)
	}
	if parent, err := CheckedSuperproject(main); err != nil || parent != "" {
		t.Fatal(parent, err)
	}
	if ran := calls(); len(ran) != 0 {
		t.Fatalf("plain checkout ran git: %q", ran)
	}
}

// A Git command that fails is told apart from a folder outside Git and from a
// repository that is no submodule.
func TestCheckedLookupsReportGitFailure(t *testing.T) {
	base := t.TempDir()
	child, parent := filepath.Join(base, "child"), filepath.Join(base, "parent")
	for _, dir := range []string{child, parent} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "init", "-q")
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "x")
	}
	git(t, parent, "submodule", "add", "-q", child, "sm")
	sm := filepath.Join(parent, "sm")
	outside := t.TempDir()
	failGit(t)
	if got, err := CheckedStateRoot(outside); err != nil || got != outside {
		t.Fatal("a folder outside Git is no failure", got, err)
	}
	if got, err := CheckedSuperproject(outside); err != nil || got != "" {
		t.Fatal("a folder outside Git is no submodule", got, err)
	}
	if _, err := CheckedStateRoot(sm); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatal("state root hid a Git failure", err)
	}
	if _, err := CheckedSuperproject(sm); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatal("superproject hid a Git failure", err)
	}
}

func TestStateRootOfALinkedWorktreeOfABareRepositoryIsItself(t *testing.T) {
	base := t.TempDir()
	src, bare, linked := filepath.Join(base, "src"), filepath.Join(base, "bare.git"), filepath.Join(base, "linked")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, src, "init", "-q")
	git(t, src, "commit", "-q", "--allow-empty", "-m", "x")
	git(t, base, "clone", "-q", "--bare", src, bare)
	git(t, bare, "worktree", "add", "-q", "--detach", linked)
	same(t, StateRoot(linked), linked)
}

func TestStateRootOfALinkedWorktreeIsTheMainCheckout(t *testing.T) {
	base := t.TempDir()
	main, linked := filepath.Join(base, "main"), filepath.Join(base, "linked")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q")
	git(t, main, "commit", "-q", "--allow-empty", "-m", "x")
	git(t, main, "worktree", "add", "-q", linked)
	same(t, StateRoot(linked), main)
}

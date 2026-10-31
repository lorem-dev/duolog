package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "protocol.file.allow=always", "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
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

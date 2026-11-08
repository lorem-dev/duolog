package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeListSkipsBareAndPrunableEntries(t *testing.T) {
	list := "worktree /a\x00HEAD 1\x00branch refs/heads/x\x00\x00" +
		"worktree /b\x00bare\x00\x00" +
		"worktree /c\x00HEAD 2\x00detached\x00prunable gitdir file points to non-existent location\x00\x00" +
		"worktree /d\nworktree /e\x00HEAD 3\x00\x00"
	got := parseWorktrees(list)
	want := []string{"/a", "/d\nworktree /e"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("%q", got)
	}
}

// A folder under a repository, with the ceiling set between them, is in none.
func TestWorkRootStopsAtTheCeiling(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, parent, "init", "-q")
	sub := filepath.Join(parent, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", parent)
	if got := WorkRoot(sub, sub); got != sub {
		t.Fatalf("work root %s, want %s", got, sub)
	}
}

// With no environment of its own, Git answers for the folder it is given and
// not for the repository that the caller's variables name.
func TestGitIgnoresGitVariablesOfTheCaller(t *testing.T) {
	other, wanted := t.TempDir(), t.TempDir()
	git(t, other, "init", "-q")
	git(t, wanted, "init", "-q")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	out, err := Git(context.Background(), nil, "-C", wanted, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	same(t, strings.TrimSpace(string(out)), filepath.Join(wanted, ".git"))
}

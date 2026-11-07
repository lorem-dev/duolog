package project

import "testing"

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

func TestCleanGitEnvDropsEveryGitVariable(t *testing.T) {
	got := cleanGitEnv([]string{"PATH=/bin", "GIT_DIR=/x", "git_work_tree=/y", "HOME=/h", "GIT_CEILING_DIRECTORIES=/"})
	if len(got) != 2 || got[0] != "PATH=/bin" || got[1] != "HOME=/h" {
		t.Fatalf("%q", got)
	}
}

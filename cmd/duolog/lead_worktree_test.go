package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/store"
)

func realGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// resolveFrom resolves a lead the way a command run in cwd does.
func resolveFrom(t *testing.T, cwd, explicit string) (session.Lead, error) {
	t.Helper()
	t.Chdir(cwd)
	root, err := projectRoot()
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	return reg.ResolveLead(explicit)
}

func TestLeadOfMainCheckoutIsFoundFromLinkedWorktree(t *testing.T) {
	t.Setenv("DUOLOG_LEAD", "")
	repo := t.TempDir()
	realGit(t, repo, "init", "-q", "-b", "main")
	realGit(t, repo, "commit", "-q", "--allow-empty", "-m", "i")
	linked := filepath.Join(repo, ".worktrees", "s5-w0")
	realGit(t, repo, "worktree", "add", "-q", linked)
	other := t.TempDir()
	realGit(t, other, "init", "-q", "-b", "main")

	t.Chdir(repo)
	s, err := store.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-123456", PID: os.Getpid(), Target: config.Target{Agent: "claude", Model: "m"}, Mode: mode.Ask}); err != nil {
		t.Fatal(err)
	}
	for _, explicit := range []string{"", "lead-123456"} {
		l, err := resolveFrom(t, linked, explicit)
		if err != nil || l.ID != "lead-123456" {
			t.Fatalf("from worktree, explicit=%q: %v %v", explicit, l, err)
		}
	}
	if _, err := resolveFrom(t, other, ""); err == nil {
		t.Fatal("a lead of another repository matched")
	}
	if _, err := resolveFrom(t, other, "lead-123456"); err == nil {
		t.Fatal("a lead of another repository matched by id")
	}
}

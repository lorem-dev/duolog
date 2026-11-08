package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func duologProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".duolog"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".duolog", "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEnabledInDuologProject(t *testing.T) {
	dir := duologProject(t)
	if ok, why := Enabled(nil, dir); !ok {
		t.Fatalf("Enabled = false (%s), want true", why)
	}
}

func TestEnabledInLinkedWorktreeOfDuologProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	dir := duologProject(t)
	git := func(args ...string) {
		t.Helper()
		base := []string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}
		if out, err := exec.Command("git", append(base, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(dir, ".worktrees", "x")
	git("worktree", "add", "-q", "-b", "x", wt)
	if ok, why := Enabled(nil, wt); !ok {
		t.Fatalf("Enabled in linked worktree = false (%s), want true", why)
	}
}

func TestDisabledOutsideDuologProject(t *testing.T) {
	ok, why := Enabled(nil, t.TempDir())
	if ok {
		t.Fatal("Enabled outside a duolog project")
	}
	if !strings.Contains(why, "duolog install --scope project") {
		t.Fatalf("line %q does not name the next step", why)
	}
}

func TestDisabledUnderWorker(t *testing.T) {
	dir := duologProject(t)
	if ok, _ := Enabled([]string{"DUOLOG_DEPTH=1"}, dir); ok {
		t.Fatal("Enabled under a worker")
	}
	if ok, _ := Enabled([]string{"DUOLOG_DEPTH=0"}, dir); !ok {
		t.Fatal("DUOLOG_DEPTH=0 must stay a lead")
	}
}

func TestDisabledUnderPeer(t *testing.T) {
	dir := duologProject(t)
	ok, why := Enabled([]string{"DUOLOG_PEER=peer-abc"}, dir)
	if ok {
		t.Fatal("Enabled under a peer")
	}
	if why == "" {
		t.Fatal("no reason given")
	}
}

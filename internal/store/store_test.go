package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesStoreAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, ".duolog"); s.Dir != want {
		t.Fatalf("Dir = %q, want %q", s.Dir, want)
	}
	path := filepath.Join(s.Dir, ".gitignore")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != GitIgnore {
		t.Fatalf(".gitignore = %q, %v", got, err)
	}
	after, err := os.Stat(path)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("Open rewrote .gitignore: %v", err)
	}
}

func TestOpenKeepsAProjectsGitIgnore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".duolog", ".gitignore")
	custom := "*\n!.gitignore\n!config.yaml\n!sync.lock.json\n!notes.md\n"
	if err := os.WriteFile(path, []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != custom {
		t.Fatalf("Open changed a project's .gitignore: %q, %v", got, err)
	}
}

func TestGitIgnoreKeepsOnlySharedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	ignored := func(name string) bool {
		cmd := exec.Command("git", "-C", root, "check-ignore", "-q", ".duolog/"+name)
		return cmd.Run() == nil
	}
	for _, name := range []string{".gitignore", "config.yaml", "sync.lock.json"} {
		if ignored(name) {
			t.Errorf("%s must stay in Git", name)
		}
	}
	for _, name := range []string{
		"bus.ndjson", "bus.lock", "sessions.json", "usage.ndjson", "decisions.ndjson", "models.json",
		"sync.lock", "config.local.yaml", "tasks/a/task.md", "cursors/a", "artifacts/a", "cache/a",
		"worktrees/a/sync.lock.json",
	} {
		if !ignored(name) {
			t.Errorf("%s must stay out of Git", strings.TrimSpace(name))
		}
	}
}

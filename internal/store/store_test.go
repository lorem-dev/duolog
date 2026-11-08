package store

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// The calibration project part goes to Git with the project; runs and other
// calibration files stay local.
func TestGitIgnoreKeepsCalibrationProjectPart(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"calibration/project.yaml", "calibration/runs/r1/run.json"} {
		path := filepath.Join(s.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ignored := func(t *testing.T, root, rel string) bool {
		t.Helper()
		err := exec.Command("git", "-C", root, "check-ignore", "-q", rel).Run()
		if exit := (*exec.ExitError)(nil); errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false
		}
		if err != nil {
			t.Fatalf("git check-ignore %s: %v", rel, err)
		}
		return true
	}
	if ignored(t, root, ".duolog/calibration/project.yaml") {
		t.Fatal("project part ignored")
	}
	if !ignored(t, root, ".duolog/calibration/runs/r1/run.json") {
		t.Fatal("runs not ignored")
	}
	if !ignored(t, root, ".duolog/calibration/rates.json") {
		t.Fatal("rates not ignored")
	}
}

// A delegation's folder and an artifact go once nothing has touched them for
// KeepFor; newer ones, and a folder with a recent file, stay.
func TestPruneRemovesOldTasksAndArtifacts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".duolog")
	now := time.Now()
	old := now.Add(-KeepFor - time.Hour)
	write := func(rel string, at time.Time) string {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		for p := path; p != dir; p = filepath.Dir(p) {
			if err := os.Chtimes(p, at, at); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}
	write("tasks/task-old/task.md", old)
	write("tasks/task-new/task.md", now)
	write("tasks/task-busy/task.md", old)
	write("tasks/task-busy/stdout.log", now)
	write("artifacts/oldhash", old)
	write("artifacts/newhash", now)
	write("journal/tasks/t-1/d-1/task.md", old)
	write("restart/lead-old", old)
	write("restart/lead-new", now)
	Prune(dir, now)
	for rel, want := range map[string]bool{"tasks/task-old": false, "tasks/task-new": true, "tasks/task-busy": true, "artifacts/oldhash": false, "artifacts/newhash": true, "journal/tasks/t-1": true, "restart/lead-old": false, "restart/lead-new": true} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); (err == nil) != want {
			t.Errorf("%s: kept=%v, want %v", rel, err == nil, want)
		}
	}
}

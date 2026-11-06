package delegate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/taskspec"
)

// The task copy of a linked worktree lives in the main checkout's shared
// state, outside the work root, and is read from there, also by an alias of
// that folder. Another file of the main checkout is no task copy.
func TestTaskCopyPins(t *testing.T) {
	main := t.TempDir()
	root := filepath.Join(t.TempDir(), "linked")
	for _, args := range [][]string{
		{"-C", main, "init", "-q"},
		{"-C", main, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "start"},
		{"-C", main, "worktree", "add", "-q", root},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	task := "## Goal\nx\n## Read\n- a.go\n## Write\n## Limits\n## Check\n"
	// main is spelled as t.TempDir gave it, which can be an alias of the
	// folder git names, as /var is on macOS.
	copyPath := filepath.Join(main, ".duolog", "tasks", "task-1", "task.md")
	if err := os.MkdirAll(filepath.Dir(copyPath), 0700); err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{copyPath: task, filepath.Join(root, "a.go"): "A\n", filepath.Join(main, "task.md"): task} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	work := WorkTree(root)
	from, at := taskCopy(work, copyPath)
	got, err := taskspec.Inline(work, from, at, 65536)
	if err != nil || !strings.Contains(string(got), "<read path=\"a.go\">\nA\n") {
		t.Fatalf("shared copy: %q, %v", got, err)
	}
	elsewhere := filepath.Join(main, "task.md")
	from, at = taskCopy(work, elsewhere)
	if from.Dir != root || at != elsewhere {
		t.Fatalf("a file of the main checkout is read from %s as %s", from.Dir, at)
	}
	if _, err := taskspec.Inline(work, from, at, 65536); err == nil || err.Error() != "path outside root: "+elsewhere {
		t.Fatalf("a file of the main checkout: %v", err)
	}
	// A copy in the work root is read from the work root.
	inside := filepath.Join(root, "task.md")
	if err := os.WriteFile(inside, []byte(task), 0600); err != nil {
		t.Fatal(err)
	}
	if from, at = taskCopy(work, inside); from.Dir != root || at != inside {
		t.Fatalf("a copy in the work root is read from %s as %s", from.Dir, at)
	}
}

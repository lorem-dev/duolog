package delegate

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestRunTellsBegunTheLeadAndTaskFolder(t *testing.T) {
	h := newHarness(t)
	calls := 0
	var lead, dir string
	_, err := h.runWith("implement", Options{Begun: func(l, d string) { calls++; lead, dir = l, d }})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || lead != "lead-123456" || !regexp.MustCompile(`^tasks/task-[0-9]+$`).MatchString(dir) {
		t.Fatalf("Begun called %d times with %q, %q", calls, lead, dir)
	}
	if _, err := os.Stat(filepath.Join(h.s.Dir, dir, "task.md")); err != nil {
		t.Fatal(err)
	}
}

func TestTaskFolder(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".duolog", "delegations", "d-x.json")
	got := LocatedRecord{Record: Record{TaskDir: "tasks/task-1"}, Path: path}.TaskFolder()
	if want := filepath.Join(root, ".duolog", "tasks", "task-1"); got != want {
		t.Fatalf("TaskFolder = %q, want %q", got, want)
	}
	if got := (LocatedRecord{Path: path}).TaskFolder(); got != "" {
		t.Fatalf("TaskFolder without TaskDir = %q", got)
	}
}

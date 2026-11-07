package policy

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// A cd may work or fail, so each cd adds the folders it leads to from every
// folder reached before it.
func TestCdFolders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell lines")
	}
	// Resolved, so the reading by name and the real folder are one.
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := Action{Kind: "exec", Target: "cd a && cd b; cat x", Work: work, Cwd: work}
	got, ok := cdFolders(a)
	want := []string{filepath.Join(work, "a"), filepath.Join(work, "b"), filepath.Join(work, "a", "b")}
	if !ok || !slices.Equal(got, want) {
		t.Fatalf("got %q %v, want %q", got, ok, want)
	}
	for _, command := range []string{`cd "$d" && cat x`, "cd - && cat x", "cd * && cat x"} {
		if got, ok := cdFolders(Action{Target: command, Work: work, Cwd: work}); !ok || len(got) != 0 {
			t.Errorf("%s: %q %v", command, got, ok)
		}
	}
	// More folders than the gate follows is outside.
	long := strings.Repeat("cd a; cd b; ", 5) + "cat x"
	if _, ok := cdFolders(Action{Target: long, Work: work, Cwd: work}); ok {
		t.Error("an unbounded cd chain was followed")
	}
	if !ExecOutside(Action{Kind: "exec", Target: long, Work: work, Cwd: work}) {
		t.Error("an unbounded cd chain is not outside")
	}
}

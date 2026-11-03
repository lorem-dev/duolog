package journal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewDiffIgnoresUserDisplayConfiguration(t *testing.T) {
	dir := repo(t)
	before, err := Capture(dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "nested/change.txt", "replay\n")
	after, err := Capture(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{"diff.noprefix", "true"}, {"color.diff", "always"}, {"diff.external", "no-such-diff-program"}} {
		run(t, dir, "config", kv[0], kv[1])
	}
	patch, err := Diff(dir, before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(patch, []byte("b/nested/change.txt")) || bytes.Contains(patch, []byte{27}) {
		t.Fatalf("non-portable patch: %q", patch)
	}
	wt := filepath.Join(t.TempDir(), "work")
	run(t, dir, "worktree", "add", "--detach", wt, "HEAD")
	if err := Apply(wt, patch); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(wt, "nested", "change.txt"))
	if err != nil || string(data) != "replay\n" {
		t.Fatal(string(data), err)
	}
}

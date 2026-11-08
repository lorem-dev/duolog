//go:build unix

package codex

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// readPlain reads a plain file only: a FIFO swapped in after the walk looked
// does not block it, and a link is not followed.
func TestReadPlainRefusesAFIFOAndALink(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := readPlain(fifo)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO was read")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO blocked")
	}
	plain := filepath.Join(dir, "plain")
	writeFile(t, plain, "x")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(plain, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readPlain(link); err == nil {
		t.Fatal("a link was followed")
	}
	if data, err := readPlain(plain); err != nil || string(data) != "x" {
		t.Fatalf("plain = %q, %v", data, err)
	}
}

func TestAdoptWorktreeRefusesAFIFOInTheCodexTree(t *testing.T) {
	f := newAdoptFixture(t)
	for _, dir := range []string{f.lead, f.worktree} {
		if err := syscall.Mkfifo(filepath.Join(dir, ".codex", "pipe"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	adoptRefused(t, f, ".codex/pipe")
}

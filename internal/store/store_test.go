package store

import (
	"bytes"
	"os"
	"path/filepath"
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
	want := []byte("*\n!config.yaml\n!sync.lock.json\n!.gitignore\n")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf(".gitignore = %q, %v", got, err)
	}
	after, err := os.Stat(path)
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("Open rewrote .gitignore: %v", err)
	}
}

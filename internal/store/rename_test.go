package store

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRenameRetry(t *testing.T) {
	for _, code := range []syscall.Errno{5, 32, 33} {
		calls := 0
		err := renameRetry(func() error {
			calls++
			if calls < 3 {
				return &os.LinkError{Op: "rename", Err: code}
			}
			return nil
		}, true)
		if err != nil || calls != 3 {
			t.Fatal(calls, err)
		}
	}
	calls := 0
	err := renameRetry(func() error { calls++; return os.ErrNotExist }, true)
	if calls != 1 || !errors.Is(err, os.ErrNotExist) {
		t.Fatal(calls, err)
	}
}

func TestRenameReplaces(t *testing.T) {
	dir := t.TempDir()
	old, dest := filepath.Join(dir, "temp"), filepath.Join(dir, "dest")
	if err := os.WriteFile(old, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Rename(old, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "new" {
		t.Fatal(string(data), err)
	}
}

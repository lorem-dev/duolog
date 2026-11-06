package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestLockExcludes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	first, err := LockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		second, err := LockFile(path)
		if err == nil {
			err = second()
		}
		got <- err
	}()
	select {
	case <-got:
		t.Fatal("second lock returned while the first was held")
	case <-time.After(150 * time.Millisecond):
	}
	if err := first(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second lock did not return after unlock")
	}
}

func TestLockFileUnlockTwice(t *testing.T) {
	unlock, err := LockFile(filepath.Join(t.TempDir(), "x.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("second unlock = %v", err)
	}
}

func TestTryLockFileAndContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, ok, err := TryLockFile(path)
	if err != nil || !ok {
		t.Fatalf("TryLockFile = %v, %v", ok, err)
	}
	if _, ok, err := TryLockFile(path); err != nil || ok {
		t.Fatalf("held TryLockFile = %v, %v", ok, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := LockFileContext(ctx, path, 10*time.Millisecond); err == nil {
		t.Fatal("LockFileContext took a held lock")
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	again, err := LockFileContext(context.Background(), path, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	_ = again()
}

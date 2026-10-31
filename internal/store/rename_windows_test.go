package store

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A real Windows handle without FILE_SHARE_DELETE reproduces the reader race.
func TestRenameOpenWindowsDestination(t *testing.T) {
	for _, release := range []bool{true, false} {
		dir := t.TempDir()
		old, dest := filepath.Join(dir, "temp"), filepath.Join(dir, "dest")
		if err := os.WriteFile(old, []byte("new"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
		name, err := windows.UTF16PtrFromString(dest)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- Rename(old, dest) }()
		if release {
			time.Sleep(30 * time.Millisecond)
			windows.CloseHandle(handle)
		}
		err = <-done
		if !release {
			windows.CloseHandle(handle)
		}
		data, readErr := os.ReadFile(dest)
		want := "old"
		if release {
			want = "new"
		}
		if readErr != nil || string(data) != want || (err == nil) != release {
			t.Fatal(string(data), err, readErr)
		}
	}
}

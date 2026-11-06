//go:build windows

package telemetry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A file system or redirector without the rename fails with one of the
// codes that say the request or its information class is not supported;
// nothing else falls back to the link. Sharing errors are retried by
// newStamp and then returned.
func TestRenameFailureWindows(t *testing.T) {
	checkRenameSorts(t, map[error]string{
		windows.ERROR_INVALID_FUNCTION:     "unsupported",
		windows.ERROR_NOT_SUPPORTED:        "unsupported",
		windows.ERROR_CALL_NOT_IMPLEMENTED: "unsupported",
		windows.ERROR_INVALID_PARAMETER:    "unsupported",
		windows.ERROR_ALREADY_EXISTS:       "exists",
		windows.ERROR_FILE_EXISTS:          "exists",
		windows.ERROR_ACCESS_DENIED:        "other",
		windows.ERROR_SHARING_VIOLATION:    "other",
		windows.ERROR_LOCK_VIOLATION:       "other",
		windows.ERROR_NOT_SAME_DEVICE:      "other",
		windows.ERROR_FILE_NOT_FOUND:       "other",
	})
}

// On NTFS, where the tests run, the handle rename works: it never skips, so
// a malformed FILE_RENAME_INFO, which Windows rejects with
// ERROR_INVALID_PARAMETER, fails here instead of passing as unsupported. A
// second temporary is refused with fs.ErrExist and stays, and the stamp
// keeps the first one's date.
func TestRenameNoReplaceWindows(t *testing.T) {
	dir := tempBase(t)
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	first, second := "prune.stamp.new.1.0123456789abcdef", "prune.stamp.new.2.0123456789abcdef"
	plant(t, filepath.Join(dir, first), nil)
	plant(t, filepath.Join(dir, second), nil)
	epoch := time.Unix(0, 0)
	if err := os.Chtimes(filepath.Join(dir, first), epoch, epoch); err != nil {
		t.Fatal(err)
	}
	if err := renameNoReplace(r, first, stampName); err != nil {
		t.Fatalf("rename: %v", err)
	}
	err = renameNoReplace(r, second, stampName)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("rename onto the stamp: %v, want %v", err, fs.ErrExist)
	}
	if got := mustLstat(t, filepath.Join(dir, stampName)).ModTime(); !got.Equal(epoch) {
		t.Errorf("stamp dated %v, want the epoch", got)
	}
	if exists(t, filepath.Join(dir, first)) || !exists(t, filepath.Join(dir, second)) {
		t.Error("the temporaries are not where the renames leave them")
	}
}

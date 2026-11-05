//go:build !windows

package telemetry

import (
	"os"
	"syscall"
	"testing"
)

// fakeForeignOwner makes every file look as if another user owns it.
func fakeForeignOwner(t *testing.T) {
	t.Helper()
	real := processUID
	processUID = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { processUID = real })
}

func loosen(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func isLoose(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()&0o077 != 0
}

func assertTight(t *testing.T, path string, dir bool) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o600)
	if dir {
		want = 0o700
	}
	if got := info.Mode().Perm(); got != want || info.IsDir() != dir {
		t.Errorf("%s: mode %v, want %v", path, info.Mode(), want)
	}
}

// withUmask sets the process umask for one test; the code under test must
// not depend on it.
func withUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

// heldDirLocked is true when err refuses to rename a directory someone holds
// open; Unix never refuses that.
func heldDirLocked(error) bool { return false }

// plantReparse puts a directory link at link; on Unix that is a symlink.
func plantReparse(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestRootOwnerUID(t *testing.T) {
	noOverride(t)
	path := tempBase(t)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureOwner(path, info); err != nil {
		t.Errorf("own directory refused: %v", err)
	}
	fakeForeignOwner(t)
	if err := secureOwner(path, info); err == nil {
		t.Error("foreign uid accepted")
	}
}

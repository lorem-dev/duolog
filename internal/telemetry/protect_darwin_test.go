//go:build darwin

package telemetry

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func readExclusion(t *testing.T, path string) []byte {
	t.Helper()
	buf := make([]byte, 256)
	n, err := unix.Lgetxattr(path, backupExcludeXattr, buf)
	if err != nil {
		t.Fatalf("no exclusion xattr: %v", err)
	}
	return buf[:n]
}

func TestRootBackupExclusion(t *testing.T) {
	noOverride(t)

	// The value tmutil addexclusion writes: a binary plist holding the
	// string com.apple.backupd.
	want := []byte("bplist00_\x10\x11com.apple.backupd\x08" +
		"\x00\x00\x00\x00\x00\x00\x01\x01" +
		"\x00\x00\x00\x00\x00\x00\x00\x01" +
		"\x00\x00\x00\x00\x00\x00\x00\x00" +
		"\x00\x00\x00\x00\x00\x00\x00\x1c")
	if !bytes.Equal(backupExcludeValue, want) {
		t.Fatalf("exclusion value %q, want %q", backupExcludeValue, want)
	}

	root := filepath.Join(tempBase(t), "telemetry.noindex")
	r, p, err := openProtected(root)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if !p.BackupVerified || !p.IndexVerified {
		t.Errorf("protection %+v, want backup and index verified", p)
	}
	if got := readExclusion(t, root); !bytes.Equal(got, want) {
		t.Errorf("xattr %q, want %q", got, want)
	}

	// A wrong value is replaced; a second run keeps the right one.
	if err := unix.Lsetxattr(root, backupExcludeXattr, []byte("wrong"), 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := excludeBackup(root); err != nil || !ok {
		t.Fatalf("excludeBackup: %v, %v", ok, err)
	}
	if got := readExclusion(t, root); !bytes.Equal(got, want) {
		t.Errorf("xattr not repaired: %q", got)
	}
	if ok, err := excludeBackup(root); err != nil || !ok {
		t.Fatalf("second excludeBackup: %v, %v", ok, err)
	}

	// Without .noindex Spotlight is not kept out.
	plain := filepath.Join(tempBase(t), "telemetry")
	r, p, err = openProtected(plain)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if p.IndexVerified || !p.BackupVerified {
		t.Errorf("plain root protection %+v", p)
	}

	// A symlinked root gets no xattr through the link.
	real := filepath.Join(tempBase(t), "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tempBase(t), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if ok, err := excludeBackup(link); err == nil || ok {
		t.Errorf("excludeBackup through a symlink: %v, %v", ok, err)
	}
	if _, err := unix.Lgetxattr(real, backupExcludeXattr, make([]byte, 64)); err == nil {
		t.Error("xattr set through the symlink")
	}
}

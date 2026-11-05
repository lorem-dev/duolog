//go:build darwin

package telemetry

import (
	"bytes"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// backupExcludeXattr is the attribute tmutil addexclusion sets; Time Machine
// skips an item that carries it. duolog writes it itself and runs no tmutil.
const backupExcludeXattr = "com.apple.metadata:com_apple_backup_excludeItem"

// backupExcludeValue is the value tmutil writes: a binary plist holding the
// string "com.apple.backupd".
var backupExcludeValue = []byte("bplist00" +
	"_\x10\x11com.apple.backupd" + // ASCII string, length 17
	"\x08" + // offset table: the one object starts at byte 8
	"\x00\x00\x00\x00\x00\x00\x01\x01" + // trailer: 1-byte offsets and refs
	"\x00\x00\x00\x00\x00\x00\x00\x01" + // one object
	"\x00\x00\x00\x00\x00\x00\x00\x00" + // the top object is object 0
	"\x00\x00\x00\x00\x00\x00\x00\x1c") // the offset table is at byte 28

// excludeBackup sets the Time Machine exclusion on the root directory
// itself, never through a symlink, and reads it back.
func excludeBackup(root string) (bool, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, scrub(err)
	}
	defer unix.Close(fd)
	if excluded(fd) {
		return true, nil
	}
	if err := unix.Fsetxattr(fd, backupExcludeXattr, backupExcludeValue, 0); err != nil {
		return false, scrub(err)
	}
	return excluded(fd), nil
}

func excluded(fd int) bool {
	buf := make([]byte, len(backupExcludeValue)+1)
	n, err := unix.Fgetxattr(fd, backupExcludeXattr, buf)
	return err == nil && bytes.Equal(buf[:n], backupExcludeValue)
}

// indexExcluded: Spotlight skips a directory whose name ends in .noindex.
func indexExcluded(root string) bool {
	return strings.HasSuffix(filepath.Base(root), ".noindex")
}

// Only Linux checks the mount; macOS roots stay in the home directory.
func refuseMount(string) error { return nil }

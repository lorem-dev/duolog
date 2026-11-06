//go:build linux

package telemetry

import (
	"syscall"
	"testing"
)

// renameat2 fails with ENOSYS on a kernel without it and with EINVAL on a
// file system without RENAME_NOREPLACE; nothing else falls back to the
// link. EXDEV cannot happen inside one root and is an error like any other.
func TestRenameFailureLinux(t *testing.T) {
	checkRenameSorts(t, map[error]string{
		syscall.ENOSYS:  "unsupported",
		syscall.EINVAL:  "unsupported",
		syscall.EEXIST:  "exists",
		syscall.ENOTSUP: "other",
		syscall.EACCES:  "other",
		syscall.EPERM:   "other",
		syscall.EIO:     "other",
		syscall.EXDEV:   "other",
		syscall.ENOENT:  "other",
	})
}

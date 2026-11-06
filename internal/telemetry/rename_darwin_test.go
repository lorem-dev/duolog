//go:build darwin

package telemetry

import (
	"syscall"
	"testing"
)

// renameatx_np without RENAME_EXCL support fails with ENOTSUP, and an older
// system with ENOSYS or EINVAL; nothing else falls back to the link.
func TestRenameFailureDarwin(t *testing.T) {
	checkRenameSorts(t, map[error]string{
		syscall.ENOTSUP: "unsupported",
		syscall.ENOSYS:  "unsupported",
		syscall.EINVAL:  "unsupported",
		syscall.EEXIST:  "exists",
		syscall.EACCES:  "other",
		syscall.EPERM:   "other",
		syscall.EIO:     "other",
		syscall.EXDEV:   "other",
		syscall.ENOENT:  "other",
	})
}

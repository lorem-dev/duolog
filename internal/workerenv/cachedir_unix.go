//go:build !windows

package workerenv

import (
	"io/fs"
	"os"
	"syscall"
)

// windowsModes is true where mode bits say nothing about access.
const windowsModes = false

// ownedByUser reports whether the folder belongs to the current user.
func ownedByUser(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

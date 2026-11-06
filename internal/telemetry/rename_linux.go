//go:build linux

package telemetry

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames oldname to newname in the root with renameat2 and
// RENAME_NOREPLACE; an existing newname fails with EEXIST.
func renameNoReplace(r *os.Root, oldname, newname string) error {
	d, err := r.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	fd := int(d.Fd())
	return renameFailure(unix.Renameat2(fd, oldname, fd, newname, unix.RENAME_NOREPLACE))
}

// renameFailure marks the errors renameat2 documents for a kernel without
// the call, ENOSYS, and for a file system without RENAME_NOREPLACE, EINVAL,
// as missing support; every other error stays as it is. EXDEV cannot happen
// inside one root.
func renameFailure(err error) error {
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return unsupported(err)
	}
	return err
}

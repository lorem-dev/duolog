//go:build darwin

package telemetry

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames oldname to newname in the root with renameatx_np,
// the directory-relative renamex_np, and RENAME_EXCL; an existing newname
// fails with EEXIST.
func renameNoReplace(r *os.Root, oldname, newname string) error {
	d, err := r.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	fd := int(d.Fd())
	return renameFailure(unix.RenameatxNp(fd, oldname, fd, newname, unix.RENAME_EXCL))
}

// renameFailure marks the errors renameatx_np documents for a file system
// without RENAME_EXCL, ENOTSUP, and for a system without the call, ENOSYS
// or EINVAL, as missing support; every other error stays as it is.
func renameFailure(err error) error {
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return unsupported(err)
	}
	return err
}

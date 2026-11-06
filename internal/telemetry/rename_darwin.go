//go:build darwin

package telemetry

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames oldname to newname in the root with renameatx_np,
// the directory-relative renamex_np, and RENAME_EXCL; an existing newname
// fails with EEXIST. A file system without the flag fails with ENOTSUP, and
// newStamp falls back.
func renameNoReplace(r *os.Root, oldname, newname string) error {
	d, err := r.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	fd := int(d.Fd())
	return unix.RenameatxNp(fd, oldname, fd, newname, unix.RENAME_EXCL)
}

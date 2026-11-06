//go:build linux

package telemetry

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames oldname to newname in the root with renameat2 and
// RENAME_NOREPLACE; an existing newname fails with EEXIST. A kernel or file
// system without the flag fails with ENOSYS or EINVAL, and newStamp falls
// back.
func renameNoReplace(r *os.Root, oldname, newname string) error {
	d, err := r.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	fd := int(d.Fd())
	return unix.Renameat2(fd, oldname, fd, newname, unix.RENAME_NOREPLACE)
}

//go:build windows

package telemetry

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// renameNoReplace renames oldname to newname in the root with MoveFileEx
// and no MOVEFILE_REPLACE_EXISTING; an existing newname fails with
// ERROR_ALREADY_EXISTS or ERROR_FILE_EXISTS, which match fs.ErrExist.
func renameNoReplace(r *os.Root, oldname, newname string) error {
	from, err := windows.UTF16PtrFromString(filepath.Join(r.Name(), filepath.FromSlash(oldname)))
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(filepath.Join(r.Name(), filepath.FromSlash(newname)))
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, 0)
}

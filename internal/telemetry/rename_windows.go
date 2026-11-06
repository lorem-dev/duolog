//go:build windows

package telemetry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// renameNoReplace renames oldname to newname, both plain names in the root,
// with MoveFileEx and no MOVEFILE_REPLACE_EXISTING, so an existing newname
// fails with fs.ErrExist and is never replaced.
//
// The two paths are built from the root's name. A handle-anchored rename
// (SetFileInformationByHandle with FILE_RENAME_INFO) was tried and failed on
// the Windows CI runners, so this one resolves the root's path once more.
// Only a process of the same user could retarget a junction above the root
// in between, and such a process can already change that user's telemetry.
func renameNoReplace(r *os.Root, oldname, newname string) error {
	if strings.ContainsAny(oldname+newname, `/\:`) {
		return errBadName
	}
	from, err := windows.UTF16PtrFromString(filepath.Join(r.Name(), oldname))
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(filepath.Join(r.Name(), newname))
	if err != nil {
		return err
	}
	return renameFailure(windows.MoveFileEx(from, to, 0))
}

// renameFailure maps a Windows rename error: an existing destination is
// fs.ErrExist, and only the codes that mean the call or its flags are not
// supported here enter the link fallback; every other error is returned.
func renameFailure(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS), errors.Is(err, windows.ERROR_FILE_EXISTS):
		return fs.ErrExist
	case errors.Is(err, windows.ERROR_INVALID_FUNCTION), errors.Is(err, windows.ERROR_NOT_SUPPORTED),
		errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED):
		return unsupported(err)
	}
	return err
}

//go:build windows

package telemetry

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileRenameInfo is FILE_RENAME_INFO with room for a short name. For
// FileRenameInfo the first field is ReplaceIfExists, which stays false.
type fileRenameInfo struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32 // in bytes, without the terminator
	FileName       [64]uint16
}

// renameNoReplace renames oldname to newname, both plain names in the root,
// through handles only, so a path that now leads elsewhere, as through an
// ancestor junction pointed at another tree, is never resolved again. It
// opens the root's directory and oldname relative to it, then renames the
// file by its handle with SetFileInformationByHandle and FILE_RENAME_INFO,
// ReplaceIfExists false and RootDirectory empty: SetFileInformationByHandle
// refuses a RootDirectory ("The parameter is incorrect"), and a FileName
// that is a plain name renames the file inside the directory it is in,
// which is the root it was opened through. An existing newname fails with
// fs.ErrExist.
func renameNoReplace(r *os.Root, oldname, newname string) error {
	if strings.ContainsAny(oldname+newname, `/\:`) {
		return errBadName
	}
	d, err := r.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	dir := windows.Handle(d.Fd())
	f, err := openForRename(dir, oldname)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(f) //nolint:errcheck // a handle opened for the rename only; nothing was written
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(f, &info); err != nil {
		return err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return errUnsafe
	}
	name, err := windows.UTF16FromString(newname)
	if err != nil {
		return err
	}
	var rename fileRenameInfo
	if len(name) > len(rename.FileName) {
		return errBadName
	}
	rename.FileNameLength = uint32(2 * (len(name) - 1)) //nolint:gosec // G115: at most 64 characters, checked above
	copy(rename.FileName[:], name)
	return renameFailure(windows.SetFileInformationByHandle(f, windows.FileRenameInfo,
		(*byte)(unsafe.Pointer(&rename)), uint32(unsafe.Sizeof(rename)))) //nolint:gosec // G103: FILE_RENAME_INFO is passed as bytes, which is how the API takes it
}

// openForRename opens name, a plain name in dir, relative to dir's handle
// with the right to rename it. A link at name is opened itself, never
// followed.
func openForRename(dir windows.Handle, name string) (windows.Handle, error) {
	object, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{RootDirectory: dir, ObjectName: object, Attributes: windows.OBJ_CASE_INSENSITIVE}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var h windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, windows.DELETE|windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES, &attrs, &status, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT, 0, 0)
	var nt windows.NTStatus
	if errors.As(err, &nt) {
		return 0, nt.Errno() // the Windows error, which errors.Is matches to fs errors
	}
	if err != nil {
		return 0, err
	}
	return h, nil
}

// renameFailure maps the codes of an existing name to fs.ErrExist, and
// marks the codes that say the request or its information class is not
// supported as missing support: ERROR_INVALID_FUNCTION and
// ERROR_NOT_SUPPORTED from a file system or redirector without the rename,
// ERROR_CALL_NOT_IMPLEMENTED from a system without the call and
// ERROR_INVALID_PARAMETER from one that does not know the information
// class. Every other error, sharing errors included, stays as it is.
func renameFailure(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS), errors.Is(err, windows.ERROR_FILE_EXISTS):
		return fs.ErrExist
	case errors.Is(err, windows.ERROR_INVALID_FUNCTION), errors.Is(err, windows.ERROR_NOT_SUPPORTED),
		errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED), errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return unsupported(err)
	}
	return err
}

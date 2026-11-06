//go:build !windows

package telemetry

import (
	"os"
	"syscall"
)

// processUID is the user every telemetry file must belong to; tests fake a
// foreign one.
var processUID = os.Geteuid

func secureOwner(path string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != processUID() {
		return errOwner
	}
	return nil
}

func singleLink(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}

// Unix has no reparse points; symlinks show in the mode.
func reparse(os.FileInfo) bool { return false }

func fileInfo(f *os.File) (os.FileInfo, error) { return f.Stat() }

func unwrap(info os.FileInfo) os.FileInfo { return info }

func ownedFile(_ *os.File, info os.FileInfo) error { return secureOwner("", info) }

// tighten sets 0700 on a directory and 0600 on a file through the handle.
func tighten(_ *os.Root, _ string, f *os.File, info os.FileInfo) error {
	want := os.FileMode(0o600)
	if info.IsDir() {
		want = 0o700
	}
	if info.Mode().Perm() == want {
		return nil
	}
	return scrub(f.Chmod(want))
}

// openRootDir opens root and confirms it is the directory info describes.
func openRootDir(root string, info os.FileInfo) (*os.Root, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, scrub(err)
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(opened, info) {
		r.Close()
		return nil, errUnsafe
	}
	return r, nil
}

// tightenRoot sets 0700 on the root through a handle confirmed to be the
// directory that was checked.
func tightenRoot(root string, info os.FileInfo) (bool, error) {
	if info.Mode().Perm() == 0o700 {
		return false, nil
	}
	r, err := openRootDir(root, info)
	if err != nil {
		return false, err
	}
	defer r.Close()
	if err := r.Chmod(".", 0o700); err != nil {
		return false, scrub(err)
	}
	return true, nil
}

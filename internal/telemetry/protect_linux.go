//go:build linux

package telemetry

import (
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// cacheDirTag marks the root for backup tools that honour the Cache
// Directory Tagging spec (borg, restic, tar --exclude-caches).
const cacheDirTag = "Signature: 8a477f597d28d172789f06886806bc55\n" +
	"# This file is a cache directory tag created by duolog.\n" +
	"# Backup tools that honour it skip this directory.\n"

// statfs is replaced in tests to fake an NFS mount.
var statfs = unix.Statfs

// excludeBackup writes CACHEDIR.TAG in the root, through the same checks as
// every other telemetry file, and reports it when the content is right.
func excludeBackup(root string) (bool, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return false, scrub(err)
	}
	if linked(info) || !info.IsDir() {
		return false, errUnsafe
	}
	r, err := openRootDir(root, info)
	if err != nil {
		return false, err
	}
	defer r.Close()
	f, err := openRegular(r, "CACHEDIR.TAG", os.O_RDWR|os.O_CREATE)
	if err != nil {
		return false, err
	}
	defer f.Close()
	got, err := io.ReadAll(io.LimitReader(f, int64(len(cacheDirTag))+1))
	if err != nil {
		return false, scrub(err)
	}
	if string(got) == cacheDirTag {
		return true, nil
	}
	if err := f.Truncate(0); err != nil {
		return false, scrub(err)
	}
	if _, err := f.WriteAt([]byte(cacheDirTag), 0); err != nil {
		return false, scrub(err)
	}
	return true, nil
}

// Linux has no search index duolog can opt out of.
func indexExcluded(string) bool { return false }

// refuseMount refuses NFS, where flock is unreliable.
func refuseMount(root string) error {
	var buf unix.Statfs_t
	if err := statfs(nearestExisting(root), &buf); err != nil {
		return scrub(err)
	}
	if int64(buf.Type) == unix.NFS_SUPER_MAGIC { //nolint:unconvert // Statfs_t.Type is int32 on 32-bit Linux
		return errNFS
	}
	return nil
}

// nearestExisting is p or its closest existing ancestor: a root not yet
// created lands on the file system of its parent.
func nearestExisting(p string) string {
	for {
		if _, err := os.Lstat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

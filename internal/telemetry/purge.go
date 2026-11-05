package telemetry

import (
	"errors"
	"io/fs"
)

// errPurge is the closed error of a Purge that failed for another reason than
// an unsafe store or a denied permission. The salt stays until every
// partition is gone, so running Purge again finishes the job.
var errPurge = errors.New("telemetry: purge failed")

// Purge deletes telemetry history. With all unset it deletes this project's
// partition, named from the salt now on disk whether or not the project or
// collection is on; the account partition, other projects and the salt stay.
// With all set it deletes every partition and then the salt, last, so no id
// from it is used again: a store opened before finds the salt gone under the
// global lock at its next append and writes under a new one. Balance keeps
// its state outside the root and is never touched.
//
// Purge holds the global lock, which it leaves in place, and takes each
// partition's lock after it, so no writer waits on a lock file it removes.
// It removes only regular files named as segments or temporary files, lock
// files and empty directories: links and unrelated files stay, nothing is
// followed and nothing outside the root is reached. A file that cannot be
// removed, as Windows refuses for one open in another process, fails the
// purge with the salt kept, and Purge can run again. Purge creates no
// history, and a store with no root purges nothing.
func (s *Store) Purge(all bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	l, _, err := s.lockGlobal()
	if err != nil {
		return purgeCategory(err)
	}
	defer l.Close()
	race("purge")
	if !all {
		id, ok, err := s.storedProject()
		if err != nil || !ok {
			return purgeCategory(err)
		}
		return purgeCategory(s.purgePartition(id))
	}
	projects, err := projectPartitions(s.root)
	if err != nil {
		return purgeCategory(err)
	}
	var failed error
	for _, p := range append([]string{accountPartition}, projects...) {
		if err := s.purgePartition(p); err != nil && failed == nil {
			failed = err
		}
	}
	if failed != nil {
		return purgeCategory(failed)
	}
	if info, err := s.root.Lstat("v1"); err == nil && info.IsDir() && !linked(info) {
		// Root.Remove removes only an empty directory.
		_ = retried(func() error { return s.root.Remove("v1") })
	}
	return purgeCategory(s.purgeFile(saltName))
}

// storedProject is this project's partition under the salt now on disk; ok
// is false outside a project or with no whole salt.
func (s *Store) storedProject() (id string, ok bool, err error) {
	if s.o.CanonicalStateRoot == "" {
		return "", false, nil
	}
	salt, n, err := readSalt(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil || n != saltSize {
		return "", false, err
	}
	return ProjectID(salt, s.o.CanonicalStateRoot), true, nil
}

// purgePartition removes a partition's segments and temporary files under
// its lock, then the lock file and the directory when nothing else is left.
// A partition that is missing, a link or not a directory is left as it is.
// It goes on past a file it cannot remove and returns the first error.
func (s *Store) purgePartition(p string) error {
	dir := "v1/" + p
	info, err := s.root.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return scrub(err)
	}
	if linked(info) || !info.IsDir() {
		return nil
	}
	l, _, err := s.lockFile(dir + "/" + lockName)
	if err != nil {
		return err
	}
	defer l.Close()
	entries, err := listDir(s.root, dir)
	if err != nil {
		return err
	}
	var failed error
	for _, e := range entries {
		if !history(e.Name()) {
			continue
		}
		if err := s.purgeFile(dir + "/" + e.Name()); err != nil && failed == nil {
			failed = err
		}
	}
	if failed != nil {
		return failed
	}
	s.removeEmpty(dir, l)
	return nil
}

// history is a segment or a temporary file of a compression.
func history(name string) bool {
	if g := segmentName.FindStringSubmatch(name); g != nil {
		return validDay(g[1])
	}
	if g := tempName.FindStringSubmatch(name); g != nil {
		return validDay(g[1])
	}
	return false
}

// purgeFile removes name when it is a regular file; a link or anything else
// stays, unfollowed, and a missing file is already gone. Removing a hard link
// removes only this name.
func (s *Store) purgeFile(name string) error {
	info, err := s.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return scrub(err)
	}
	if !regular(info) {
		return nil
	}
	return scrub(retried(func() error { return s.root.Remove(name) }))
}

// purgeCategory maps a failed check or step to a closed error.
func purgeCategory(err error) error {
	var refused *refusedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errUnsafe), errors.Is(err, errOwner), errors.As(err, &refused):
		return errUnsafeStore
	case errors.Is(err, fs.ErrPermission):
		return errDenied
	}
	return errPurge
}

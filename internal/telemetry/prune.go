package telemetry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"syscall"
	"time"

	"github.com/gofrs/flock"
)

// stampName is the file whose mtime records the last completed prune.
const stampName = "prune.stamp"

// Maintenance counts what one Prune did. Deleted counts the files removed:
// expired segments and temporary files a crash left. Compressed counts the
// closed days now held in gzip only. Skipped counts segments and temporary
// files left in place: links, files with another name, and steps that failed,
// which the next prune tries again. Bytes is what the pass freed on disk.
// Entries maintenance does not recognise are left alone and not counted.
type Maintenance struct {
	Deleted, Compressed, Skipped int
	Bytes                        int64
}

// errPrune is the closed error of a Prune that failed for another reason than
// an unsafe store or a denied permission.
var errPrune = errors.New("telemetry: prune failed")

// tempName is a compression a crash or a failed step left behind.
var tempName = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2})\.ndjson\.gz\.tmp$`)

// Prune deletes the segments of every partition whose day D has now >= D +
// retention, compresses each closed day before today, removes temporary files
// a crash left and empty partitions. It runs when force is set, or when
// prune.stamp is missing, at least PruneEveryHours old, or ahead of the clock;
// otherwise it does nothing. The due check runs under the stamp's flock, so
// of several due processes one prunes and the others find the stamp fresh.
// Partitions are then locked one at a time.
//
// The stamp's mtime is set to now only when a pass completes; a step that
// fails on one file is skipped and retried by the next prune. No other clock
// state is kept: a clock set back only delays deletion. Prune runs with
// collection off too, and a store with no root prunes nothing.
func (s *Store) Prune(force bool) (Maintenance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return Maintenance{}, nil
	}
	var err error
	if s.o.Enabled {
		_, err = checkRoot(s.path)
	} else {
		// A store with collection off leaves the root's protection as it is.
		_, _, err = inspectRoot(s.path)
	}
	if err != nil {
		return Maintenance{}, pruneCategory(err)
	}
	if err := sameDir(s.root, s.path); err != nil {
		return Maintenance{}, pruneCategory(err)
	}
	if err := s.newStamp(); err != nil {
		return Maintenance{}, pruneCategory(err)
	}
	l, stamp, err := s.lockFile(stampName)
	if err != nil {
		return Maintenance{}, pruneCategory(err)
	}
	defer l.Close()
	now := s.o.Now().UTC()
	if !force && !due(stamp.ModTime(), now, time.Duration(s.o.PruneEveryHours)*time.Hour) {
		return Maintenance{}, nil
	}
	race("prune")
	projects, err := projectPartitions(s.root)
	if err != nil {
		return Maintenance{}, pruneCategory(err)
	}
	var m Maintenance
	for _, p := range append([]string{accountPartition}, projects...) {
		s.prunePartition(p, now, &m)
	}
	if err := s.setStamp(stamp, now); err != nil {
		return m, pruneCategory(err)
	}
	return m, nil
}

// due is true when the last completed prune is at least every old, or ahead
// of now because the clock was set back.
func due(last, now time.Time, every time.Duration) bool {
	age := now.Sub(last)
	return age < 0 || age >= every
}

// newStamp creates a missing stamp dated at the Unix epoch, so it records no
// completed prune.
func (s *Store) newStamp() error {
	if _, err := s.root.Lstat(stampName); !errors.Is(err, fs.ErrNotExist) {
		return nil // lockFile checks what is there
	}
	f, err := openRegular(s.root, stampName, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	epoch := time.Unix(0, 0)
	return scrub(s.root.Chtimes(stampName, epoch, epoch))
}

// setStamp dates the stamp at now once it is confirmed to be the locked file.
func (s *Store) setStamp(locked os.FileInfo, now time.Time) error {
	info, err := s.root.Lstat(stampName)
	if err != nil {
		return scrub(err)
	}
	if !regular(info) || !os.SameFile(info, locked) {
		return errUnsafe
	}
	return scrub(s.root.Chtimes(stampName, now, now))
}

// lockFile takes the exclusive flock on name, a file opened and checked like
// every other, and confirms after locking that the locked file is the one
// checked and is still at name, so a file removed or replaced meanwhile is
// never trusted.
func (s *Store) lockFile(name string) (*flock.Flock, os.FileInfo, error) {
	f, err := openRegular(s.root, name, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, nil, err
	}
	checked, err := fileInfo(f)
	f.Close()
	if err != nil {
		return nil, nil, scrub(err)
	}
	// Without O_CREATE flock cannot make a file in place of a removed one.
	l := flock.New(filepath.Join(s.path, filepath.FromSlash(name)), flock.SetFlag(os.O_RDWR))
	if err := l.Lock(); err != nil {
		l.Close()
		return nil, nil, scrub(err)
	}
	locked, err := l.Stat()
	if err != nil || !os.SameFile(locked, unwrap(checked)) {
		l.Close()
		return nil, nil, errUnsafe
	}
	at, err := s.root.Lstat(name)
	if err != nil || !regular(at) || !os.SameFile(at, locked) {
		l.Close()
		return nil, nil, errUnsafe
	}
	return l, locked, nil
}

// prunePartition runs the maintenance of one partition under its lock. A
// partition that is missing is left out; one that is a link or fails a check
// is skipped.
func (s *Store) prunePartition(p string, now time.Time, m *Maintenance) {
	dir := "v1/" + p
	info, err := s.root.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil || linked(info) || !info.IsDir() {
		m.Skipped++
		return
	}
	l, _, err := s.lockFile(dir + "/" + lockName)
	if err != nil {
		m.Skipped++
		return
	}
	defer l.Close()
	entries, err := listDir(s.root, dir)
	if err != nil {
		m.Skipped++
		return
	}
	type pair struct{ plain, gz bool }
	segments := map[string]*pair{}
	for _, e := range entries {
		name := e.Name()
		// Only a prune holding this lock writes a temporary file, so any
		// one found here is stale.
		if t := tempName.FindStringSubmatch(name); t != nil && validDay(t[1]) {
			s.removeFile(dir+"/"+name, m)
			continue
		}
		g := segmentName.FindStringSubmatch(name)
		if g == nil || !validDay(g[1]) {
			continue
		}
		if segments[g[1]] == nil {
			segments[g[1]] = &pair{}
		}
		if g[2] != "" {
			segments[g[1]].gz = true
		} else {
			segments[g[1]].plain = true
		}
	}
	cutoff := now.Add(-time.Duration(s.o.RetentionDays) * 24 * time.Hour)
	today := now.Format(time.DateOnly)
	days := make([]string, 0, len(segments))
	for d := range segments {
		days = append(days, d)
	}
	slices.Sort(days)
	for _, d := range days {
		seg := segments[d]
		base := dir + "/" + d + ".ndjson"
		start, _ := time.Parse(time.DateOnly, d)
		switch {
		case !start.After(cutoff):
			if seg.gz {
				s.removeFile(base+".gz", m)
			}
			if seg.plain {
				s.removeFile(base, m)
			}
		case seg.plain && d < today:
			freed := s.sizes(base, base+".gz")
			if err := s.compress(p, d); err != nil {
				m.Skipped++
				continue
			}
			m.Compressed++
			m.Bytes += freed - s.sizes(base+".gz")
		}
	}
	s.removeEmpty(dir, l)
}

// removeFile removes name when it is a regular file: a link, directory or
// anything else is skipped, and nothing is followed. Removing a hard link
// removes only this name.
func (s *Store) removeFile(name string, m *Maintenance) {
	info, err := s.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil || !regular(info) {
		m.Skipped++
		return
	}
	if err := retried(func() error { return s.root.Remove(name) }); err != nil {
		m.Skipped++
		return
	}
	m.Deleted++
	m.Bytes += info.Size()
}

// sizes adds up the sizes of the regular files among names.
func (s *Store) sizes(names ...string) int64 {
	var n int64
	for _, name := range names {
		if info, err := s.root.Lstat(name); err == nil && regular(info) {
			n += info.Size()
		}
	}
	return n
}

// removeEmpty removes a partition that holds nothing but its lock file. On
// Unix the lock file is removed while held. Windows refuses to remove a file
// open anywhere, this lock's own handle included, so there the lock is
// released first, and a writer that opened it meanwhile keeps the partition.
func (s *Store) removeEmpty(dir string, l *flock.Flock) {
	entries, err := listDir(s.root, dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != lockName {
			return
		}
	}
	if len(entries) == 1 {
		if runtime.GOOS == "windows" {
			l.Close()
		}
		name := dir + "/" + lockName
		info, err := s.root.Lstat(name)
		if err != nil || !regular(info) {
			return
		}
		if err := retried(func() error { return s.root.Remove(name) }); err != nil {
			return
		}
	}
	// Root.Remove removes only an empty directory.
	_ = retried(func() error { return s.root.Remove(dir) })
}

// retried runs a rename or removal again while Windows reports the sharing
// errors of a file another process holds open, as store.Rename does.
func retried(op func() error) error {
	return retry(op, runtime.GOOS == "windows")
}

func retry(op func() error, windows bool) error {
	for attempt := 0; ; attempt++ {
		err := op()
		// ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION and
		// ERROR_LOCK_VIOLATION; these numbers are not Unix errnos here.
		transient := windows && (errors.Is(err, syscall.Errno(5)) || errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33)))
		if !transient || attempt == 7 {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
}

// pruneCategory maps a failed check or step to a closed error.
func pruneCategory(err error) error {
	var refused *refusedError
	switch {
	case errors.Is(err, errUnsafe), errors.Is(err, errOwner), errors.As(err, &refused):
		return errUnsafeStore
	case errors.Is(err, fs.ErrPermission):
		return errDenied
	}
	return errPrune
}

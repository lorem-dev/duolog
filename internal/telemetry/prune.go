package telemetry

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gofrs/flock"
)

// stampName is the file whose mtime records the last completed prune. Its
// flock is the global lock, and nothing removes it.
const stampName = "prune.stamp"

// Maintenance counts what one Prune did. Deleted counts the files removed:
// expired segments, segments evicted for the root cap and temporary files a
// crash left. Compressed counts the closed days now held in gzip only.
// Skipped counts segments and temporary files left in place: links, files
// with another name, and steps that failed, which the next prune tries again.
// Bytes is what the pass freed on disk. Entries maintenance does not
// recognise are left alone and not counted.
type Maintenance struct {
	Deleted, Compressed, Skipped int
	Bytes                        int64
}

// errPrune is the closed error of a Prune that failed for another reason than
// an unsafe store or a denied permission.
var errPrune = errors.New("telemetry: prune failed")

// tempName is a compression a crash or a failed step left behind.
var tempName = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2})\.ndjson\.gz\.tmp$`)

// errMoved is a lock taken on a file that is no longer at its name: a prune
// or purge removed it while the locker waited.
var errMoved = fmt.Errorf("%w: lock file moved", errUnsafe)

// Prune deletes the segments of every partition whose day D has now >= D +
// retention, compresses each closed day before today, removes temporary files
// a crash left and empty partitions, then evicts the oldest segments while
// the root is over its cap. It runs when force is set, or when prune.stamp is
// missing, at least PruneEveryHours old, or ahead of the clock; otherwise it
// does nothing. The due check runs under the global lock, so of several due
// processes one prunes and the others find the stamp fresh. Partitions are
// then locked one at a time.
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
	every := time.Duration(s.o.PruneEveryHours) * time.Hour
	// Common path: a fresh stamp is read without any lock.
	if !force && !s.stampDue(every) {
		return Maintenance{}, nil
	}
	// One caller per root waits on the file lock; the others wait here and
	// then find the stamp fresh.
	rl := rootLock(s.path)
	rl.Lock()
	defer rl.Unlock()
	if !force && !s.stampDue(every) {
		return Maintenance{}, nil
	}
	l, stamp, err := s.lockGlobal()
	if err != nil {
		return Maintenance{}, pruneCategory(err)
	}
	defer l.Close()
	now := s.o.Now().UTC()
	if !force && !due(stamp.ModTime(), now, every) { // checked again under the lock
		return Maintenance{}, nil
	}
	race("prune")
	projects, err := projectPartitions(s.root)
	if err != nil {
		return Maintenance{}, pruneCategory(err)
	}
	var m Maintenance
	s.removeStaleStamps(now, &m)
	for _, p := range append([]string{accountPartition}, projects...) {
		s.prunePartition(p, now, &m)
	}
	if _, err := s.fit(0, now.Format(time.DateOnly), "", &m); err != nil {
		m.Skipped++
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

// rootLocks holds one mutex per root path, shared by every store of this
// process on that root.
var rootLocks sync.Map

func rootLock(path string) *sync.Mutex {
	m, _ := rootLocks.LoadOrStore(path, new(sync.Mutex))
	return m.(*sync.Mutex) //nolint:errcheck // rootLocks holds nothing else
}

// stampDue reads the stamp without a lock. A missing or unreadable stamp is
// due; the check under the global lock decides for good.
func (s *Store) stampDue(every time.Duration) bool {
	info, err := s.root.Lstat(stampName)
	if err != nil || !regular(info) {
		return true
	}
	return due(info.ModTime(), s.o.Now().UTC(), every)
}

// renameStamp moves the dated temporary stamp to its name and fails with
// fs.ErrExist when a stamp is there already; tests replace it to act as a
// system or file system that cannot rename without replacing.
var renameStamp = renameNoReplace

// errRenameUnsupported is a rename that refuses to replace failing because
// the system or file system cannot do it. Each platform's renameFailure
// returns it only for the errors documented to mean that, and only it
// sends newStamp to the link.
var errRenameUnsupported = fmt.Errorf("telemetry: no rename without replace: %w", errors.ErrUnsupported)

// unsupported marks err, a rename's own error, as missing support.
func unsupported(err error) error { return fmt.Errorf("%w: %w", errRenameUnsupported, err) }

// linkStamp links the dated temporary stamp in at its name where renameStamp
// fails; tests replace it to act as a file system without hard links.
var linkStamp = func(r *os.Root, oldname, newname string) error { return r.Link(oldname, newname) }

// stampTemp is a temporary stamp: its name holds the Unix second it was made
// at, since its mtime is the epoch before it is linked.
var stampTemp = regexp.MustCompile(`^` + regexp.QuoteMeta(stampName) + `\.new\.([0-9]+)\.[0-9a-f]{16}$`)

// newStamp creates a missing stamp dated at the Unix epoch, so it records no
// completed prune. The stamp appears at its name already dated: it is made
// under a temporary name and moved there, so no one can lock it, or read it
// as due, between its creation and its date. The move is a rename that
// refuses to replace a stamp, so the stamp never has a second name, and a
// process that loses the race to another creator drops its temporary and
// uses the stamp that won. A rename Windows refuses because another process
// holds the file is tried again; any other failure but missing support is
// returned. Where the system or file system cannot rename
// that way, the temporary is linked in and then removed, and for that moment
// the stamp has two links, which openRetried waits out. A file system
// without hard links either, such as exFAT, FAT32 or some SMB shares, refuses
// the link too; there the stamp is created in place and then dated, and the
// checks under the lock decide.
func (s *Store) newStamp() error {
	if _, err := s.root.Lstat(stampName); !errors.Is(err, fs.ErrNotExist) {
		return nil // lockFile checks what is there
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return scrub(err)
	}
	tmp := fmt.Sprintf("%s.new.%d.%s", stampName, s.o.Now().Unix(), hex.EncodeToString(b[:]))
	f, err := s.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return scrub(err)
	}
	f.Close()
	// Best effort, and a no-op once renamed: removeStaleStamps clears a
	// leftover. Windows refuses to remove a linked-in name while the stamp is
	// open anywhere, so the removal is retried there.
	defer func() { _ = retried(func() error { return s.root.Remove(tmp) }) }()
	epoch := time.Unix(0, 0)
	if err := s.root.Chtimes(tmp, epoch, epoch); err != nil {
		return scrub(err)
	}
	err = retry(func() error { return renameStamp(s.root, tmp, stampName) }, sharingError)
	switch {
	case err == nil, errors.Is(err, fs.ErrExist):
		return nil
	case !errors.Is(err, errRenameUnsupported):
		return scrub(err)
	}
	err = linkStamp(s.root, tmp, stampName)
	if err == nil || errors.Is(err, fs.ErrExist) {
		return nil
	}
	f, err = openRegular(s.root, stampName, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return scrub(s.root.Chtimes(stampName, epoch, epoch))
}

// removeStaleStamps removes the temporary stamps that a process killed inside
// newStamp left, once they are more than a minute old or ahead of the clock.
// A newer one may belong to a process about to link it.
func (s *Store) removeStaleStamps(now time.Time, m *Maintenance) {
	entries, err := listDir(s.root, ".")
	if err != nil {
		m.Skipped++
		return
	}
	for _, e := range entries {
		match := stampTemp.FindStringSubmatch(e.Name())
		if match == nil {
			continue
		}
		made, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			continue
		}
		if age := now.Sub(time.Unix(made, 0)); age > time.Minute || age < -time.Minute {
			s.removeFile(e.Name(), m)
		}
	}
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

// lockGlobal checks the root and takes the global lock, the flock on
// prune.stamp. Appends, prunes and purges hold it before they take any
// partition lock, so none of them waits on a partition lock another holds
// while a purge removes it. With collection off the root's protection is
// left as it is.
func (s *Store) lockGlobal() (*flock.Flock, os.FileInfo, error) {
	var err error
	if s.o.Enabled {
		_, err = checkRoot(s.path)
	} else {
		_, _, err = inspectRoot(s.path)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := sameDir(s.root, s.path); err != nil {
		return nil, nil, err
	}
	if err := s.newStamp(); err != nil {
		return nil, nil, err
	}
	return s.lockFile(stampName)
}

// lockFile takes the exclusive flock on name, a file opened and checked like
// every other, and confirms after locking that the locked file is the one
// checked and is still at name, so a file removed or replaced meanwhile is
// never trusted. A file that left the name while the lock was awaited fails
// with errMoved.
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
	race("locked")
	at, err := s.root.Lstat(name)
	if err != nil || !regular(at) || !os.SameFile(at, locked) {
		l.Close()
		return nil, nil, errMoved
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

// removeEmpty removes a partition that holds nothing but its lock file. The
// caller holds the global lock, so no writer waits on this lock file; a
// reader that does finds it gone and locks again. On Unix the lock file is
// removed while held. Windows refuses to remove a file open anywhere, this
// lock's own handle included, so there the lock is released first, and a
// reader that opened it meanwhile keeps the partition.
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

// rootBytes adds up the size of every entry under the root but directories:
// segments, metadata, locks, temporary and unrelated files all count against
// the cap. Links count by their own size and are never followed, and the
// walk never leaves the root.
func rootBytes(r *os.Root) (int64, error) {
	var n int64
	err := fs.WalkDir(r.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		n += info.Size()
		return nil
	})
	return n, scrub(err)
}

// evictable is a segment the root cap may evict.
type evictable struct {
	partition, day, name string
	size                 int64
}

// fit makes room for n more bytes under the root cap of MaxMegabytes. When
// the root would pass it, the regular segments of days before today go,
// oldest day first across all partitions, until n fits; today's are never
// evicted. When evicting all of them would not make room, fit evicts nothing
// and reports false. The caller holds the global lock and, when held is set,
// that partition's lock; any other partition is locked for its removals.
func (s *Store) fit(n int64, today, held string, m *Maintenance) (bool, error) {
	limit := int64(s.o.MaxMegabytes) * megabyte
	total, err := rootBytes(s.root)
	if err != nil {
		return false, err
	}
	if total+n <= limit {
		return true, nil
	}
	segments, err := s.evictable(today)
	if err != nil {
		return false, err
	}
	var all int64
	for _, e := range segments {
		all += e.size
	}
	if total-all+n > limit {
		return false, nil
	}
	for _, e := range segments {
		if total+n <= limit {
			break
		}
		total -= s.evict(e, held, m)
	}
	return total+n <= limit, nil
}

// evictable lists the regular segments of days before today, oldest day
// first. A partition that is missing, a link or fails a check holds none.
func (s *Store) evictable(today string) ([]evictable, error) {
	projects, err := projectPartitions(s.root)
	if err != nil {
		return nil, err
	}
	var out []evictable
	for _, p := range append([]string{accountPartition}, projects...) {
		dir := "v1/" + p
		entries, err := listDir(s.root, dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			g := segmentName.FindStringSubmatch(e.Name())
			if g == nil || !validDay(g[1]) || g[1] >= today {
				continue
			}
			info, err := s.root.Lstat(dir + "/" + e.Name())
			if err != nil || !regular(info) {
				continue
			}
			out = append(out, evictable{partition: p, day: g[1], name: e.Name(), size: info.Size()})
		}
	}
	slices.SortFunc(out, func(a, b evictable) int {
		return cmp.Or(cmp.Compare(a.day, b.day), cmp.Compare(a.partition, b.partition), cmp.Compare(a.name, b.name))
	})
	return out, nil
}

// evict removes one segment under its partition's lock and returns the bytes
// it freed; a partition the caller holds is not locked again.
func (s *Store) evict(e evictable, held string, m *Maintenance) int64 {
	dir := "v1/" + e.partition
	if e.partition != held {
		l, _, err := s.lockFile(dir + "/" + lockName)
		if err != nil {
			m.Skipped++
			return 0
		}
		defer l.Close()
	}
	before := m.Bytes
	s.removeFile(dir+"/"+e.name, m)
	return m.Bytes - before
}

// retried runs a rename or removal again while Windows reports the sharing
// errors of a file another process holds open, or access denied for one
// being deleted, as store.Rename does.
func retried(op func() error) error {
	// ERROR_ACCESS_DENIED; not a Unix errno here.
	return retry(op, func(err error) bool { return errors.Is(err, syscall.Errno(5)) || sharingError(err) })
}

// windowsErrors is true where errors are Windows error codes; tests set it
// to run the Windows retries elsewhere.
var windowsErrors = runtime.GOOS == "windows"

// retryAttempts is how many times retry runs an operation at most.
const retryAttempts = 8

// retry runs op again, after a growing pause, while windowsErrors is set
// and transient holds for its error.
func retry(op func() error, transient func(error) bool) error {
	for attempt := 1; ; attempt++ {
		err := op()
		if !windowsErrors || !transient(err) || attempt == retryAttempts {
			return err
		}
		time.Sleep(time.Duration(attempt) * 10 * time.Millisecond)
	}
}

// sharingError is ERROR_SHARING_VIOLATION or ERROR_LOCK_VIOLATION, which
// Windows reports while another process holds the file; these numbers are
// not Unix errnos here.
func sharingError(err error) bool {
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33))
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

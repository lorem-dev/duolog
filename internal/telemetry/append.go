package telemetry

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gofrs/flock"
)

const (
	// accountPartition holds the events of the account rather than a
	// project: quota, route, and reset, which marks a new account or plan
	// epoch.
	accountPartition = "_account"
	lockName         = "telemetry.lock"
)

var (
	partitionPattern = regexp.MustCompile(`^(?:_account|[0-9a-f]{16})$`)
	dayPattern       = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// Append fails with one of these closed errors. None carries a path or the
// cause's text, so a caller can show it anywhere.
var (
	errRefused     = errors.New("telemetry: event refused")
	errUnsafeStore = errors.New("telemetry: store unsafe")
	errDenied      = errors.New("telemetry: permission denied")
	errWrite       = errors.New("telemetry: write failed")
	errClosed      = errors.New("telemetry: store closed")
)

// writeFile and closeFile let tests fail a write or a close.
var (
	writeFile = (*os.File).Write
	closeFile = (*os.File).Close
)

// Append writes e as one line to the segment of its partition for the UTC
// day of the write. With collection off, or a project event with the project
// off, it writes nothing and returns nil. A failure is counted in Health and
// returned as a closed error; the caller carries on as if it had succeeded.
// The partition lock is held only for this append, never across a vendor
// call.
func (s *Store) Append(e Event) error {
	if !s.o.Enabled {
		return nil
	}
	account, known := accountEvent(e)
	if known && !account && !s.o.ProjectEnabled {
		return nil
	}
	err := s.append(e, account, known)
	if err != nil {
		countFailure()
	}
	return err
}

func (s *Store) append(e Event, account, known bool) error {
	if !known {
		return errRefused
	}
	now := s.o.Now().UTC()
	line, err := Encode(e, s.o.Names, now)
	if err != nil {
		return errRefused
	}
	partition := s.project
	if account {
		partition = accountPartition
	}
	day := now.Format(time.DateOnly)
	if !partitionPattern.MatchString(partition) || !dayPattern.MatchString(day) || !validDay(day) {
		return errRefused
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return errClosed
	}
	return category(s.write("v1/"+partition, day, line))
}

// accountEvent sorts e into the account or a project partition; known is
// false for anything else.
func accountEvent(e Event) (account, known bool) {
	switch e.(type) {
	case Run, *Run, Gate, *Gate, Peer, *Peer:
		return false, true
	case Quota, *Quota, Route, *Route, Reset, *Reset:
		return true, true
	}
	return false, false
}

// write checks the root again, then appends under the partition lock:
// repair the tail, seek to the end, one write, close. No fsync: a crash may
// lose the last event.
func (s *Store) write(dir, day string, line []byte) error {
	if _, err := checkRoot(s.path); err != nil {
		return err
	}
	if err := sameDir(s.root, s.path); err != nil {
		return err
	}
	unlock, err := s.lock(dir + "/" + lockName)
	if err != nil {
		return err
	}
	defer unlock()
	// No O_APPEND: on Windows it opens the file without the right to
	// truncate it. The lock keeps writers apart.
	f, err := openRegular(s.root, dir+"/"+day+".ndjson", os.O_CREATE|os.O_RDWR)
	if err != nil {
		return err
	}
	race("append")
	err = appendLine(f, line)
	if cerr := closeFile(f); err == nil {
		err = cerr
	}
	return err
}

// lock takes the partition's flock on a lock file checked like every other
// file, and confirms the locked file is the one that was checked.
func (s *Store) lock(name string) (func(), error) {
	f, err := openRegular(s.root, name, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	checked, err := fileInfo(f)
	if err != nil {
		return nil, scrub(err)
	}
	// Without O_CREATE flock cannot make a file in place of a removed one.
	l := flock.New(filepath.Join(s.path, filepath.FromSlash(name)), flock.SetFlag(os.O_RDWR))
	if err := l.Lock(); err != nil {
		l.Close()
		return nil, scrub(err)
	}
	locked, err := l.Stat()
	if err != nil || !os.SameFile(locked, unwrap(checked)) {
		l.Close()
		return nil, errUnsafe
	}
	return func() { l.Close() }, nil
}

// appendLine writes line at the end of f after cutting an unterminated last
// line. A failed write is cut back too, so the segment keeps whole lines.
func appendLine(f *os.File, line []byte) error {
	if err := repairTail(f); err != nil {
		return err
	}
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	n, err := writeFile(f, line)
	if err == nil && n < len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = f.Truncate(end)
	}
	return err
}

// repairTail cuts the file back to its last newline. Every line is written
// whole with its newline, so a last line without one is an interrupted
// write, even when it happens to be valid JSON.
func repairTail(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	buf := make([]byte, 4096)
	for end := size; end > 0; {
		start := max(end-int64(len(buf)), 0)
		chunk := buf[:end-start]
		if _, err := f.ReadAt(chunk, start); err != nil {
			return err
		}
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			if keep := start + int64(i) + 1; keep < size {
				return f.Truncate(keep)
			}
			return nil
		}
		end = start
	}
	if size > 0 {
		return f.Truncate(0)
	}
	return nil
}

// category maps a store error to its closed error.
func category(err error) error {
	var refused *refusedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errBadName):
		return errRefused
	case errors.Is(err, errUnsafe), errors.Is(err, errOwner), errors.As(err, &refused):
		return errUnsafeStore
	case errors.Is(err, fs.ErrPermission):
		return errDenied
	}
	return errWrite
}

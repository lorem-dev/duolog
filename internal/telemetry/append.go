package telemetry

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"time"
)

const (
	// accountPartition holds the events of the account rather than a
	// project: quota, route, and reset, which marks a new account or plan
	// epoch.
	accountPartition = "_account"
	lockName         = "telemetry.lock"
)

// The caps, in bytes: 1 MB is 1,000,000 bytes. A project writes at most
// projectDayBytes raw per UTC day; the whole root, account included, holds at
// most MaxMegabytes.
const (
	megabyte        = 1_000_000
	projectDayBytes = 2 * megabyte
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
	errCapped      = errors.New("telemetry: cap reached")
)

// writeFile and closeFile let tests fail a write or a close.
var (
	writeFile = (*os.File).Write
	closeFile = (*os.File).Close
)

// Append writes e as one line to the segment of its partition for the UTC
// day of the write. With collection off, or a project event with the project
// off, it writes nothing and returns nil. An event over a cap is dropped
// whole: it is counted in Health as dropped and Append returns errCapped. A
// failure is counted in Health and returned as a closed error; the caller
// carries on as if it had succeeded. A due prune runs first, under its own
// locks; the global and partition locks are then held only for this append,
// never across a vendor call.
func (s *Store) Append(e Event) error {
	if !s.o.Enabled {
		return nil
	}
	account, known := accountEvent(e)
	if known && !account && !s.o.ProjectEnabled {
		return nil
	}
	err := s.append(e, account, known)
	switch {
	case errors.Is(err, errCapped):
		dropped.Add(1)
	case err != nil:
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
	day := now.Format(time.DateOnly)
	if !dayPattern.MatchString(day) || !validDay(day) {
		return errRefused
	}
	// Prune takes the locks itself, so it runs before the append's and
	// never inside them. One that fails is tried again by the next write.
	_, _ = s.Prune(false)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return errClosed
	}
	return category(s.write(account, day, line))
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

// write checks the root again, then appends under the global lock and the
// partition lock, taken in that order: repair the tail, check the caps, seek
// to the end, one write, close. No fsync: a crash may lose the last event.
func (s *Store) write(account bool, day string, line []byte) error {
	global, _, err := s.lockGlobal()
	if err != nil {
		return err
	}
	defer global.Close()
	partition := accountPartition
	if !account {
		if partition, err = s.currentProject(); err != nil {
			return err
		}
	}
	if !partitionPattern.MatchString(partition) {
		return errRefused
	}
	dir := "v1/" + partition
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
	err = s.capped(f, partition, day, account, int64(len(line)))
	if err == nil {
		err = appendLine(f, line)
	}
	if cerr := closeFile(f); err == nil {
		err = cerr
	}
	return err
}

// currentProject returns this store's project partition under the salt now
// on disk, making a new salt when a purge removed it. The caller holds the
// global lock, so a store opened before a purge writes under the new salt,
// never under an id the purge ended.
func (s *Store) currentProject() (string, error) {
	salt, err := loadSalt(s.root)
	if err != nil {
		return "", err
	}
	s.project = ProjectID(salt, s.o.CanonicalStateRoot)
	return s.project, nil
}

// lock takes the partition's flock with lockFile. A lock taken on a file that
// left the name meanwhile, as a prune or purge removes the lock of a
// partition it empties, is let go and taken once more on the file now there.
func (s *Store) lock(name string) (func(), error) {
	l, _, err := s.lockFile(name)
	if errors.Is(err, errMoved) {
		l, _, err = s.lockFile(name)
	}
	if err != nil {
		return nil, err
	}
	return func() { l.Close() }, nil
}

// capped repairs the tail of f, the segment of partition for day, and
// returns errCapped when n more bytes would take a project's day past
// projectDayBytes raw, or the root past its cap once fit has evicted what it
// can. The bytes a repair cut never count; every byte that survives does.
// The caller holds the global lock and the partition's lock.
func (s *Store) capped(f *os.File, partition, day string, account bool, n int64) error {
	if err := repairTail(f); err != nil {
		return err
	}
	if !account {
		raw, err := s.dayBytes(f, "v1/"+partition+"/"+day+".ndjson.gz")
		if err != nil {
			return err
		}
		if raw+n > projectDayBytes {
			return errCapped
		}
	}
	fits, err := s.fit(n, day, partition, &Maintenance{})
	if err != nil {
		return err
	}
	if !fits {
		return errCapped
	}
	return nil
}

// dayBytes is the raw size of a project's day: the plain segment f plus what
// a gzip copy of the same day holds, which a clock set back can leave.
func (s *Store) dayBytes(f *os.File, gz string) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	g, err := openReading(s.root, gz)
	if errors.Is(err, fs.ErrNotExist) {
		return info.Size(), nil
	}
	if err != nil {
		return 0, err
	}
	defer g.Close()
	return info.Size() + rawSize(g, projectDayBytes+1), nil
}

// rawSize is how many bytes the gzip file f holds, counted up to limit. A
// copy that does not decompress counts at least its size on disk.
func rawSize(f *os.File, limit int64) int64 {
	var size int64
	if info, err := f.Stat(); err == nil {
		size = info.Size()
	}
	z, err := gzip.NewReader(f)
	if err != nil {
		return size
	}
	defer z.Close()
	n, err := io.Copy(io.Discard, io.LimitReader(z, limit))
	if err != nil {
		return max(n, size)
	}
	return n
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
	case errors.Is(err, errCapped):
		return errCapped
	case errors.Is(err, errBadName):
		return errRefused
	case errors.Is(err, errUnsafe), errors.Is(err, errOwner), errors.As(err, &refused):
		return errUnsafeStore
	case errors.Is(err, fs.ErrPermission):
		return errDenied
	}
	return errWrite
}

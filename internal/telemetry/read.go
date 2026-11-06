package telemetry

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/gofrs/flock"
)

// Query selects what Read returns. Since only narrows the retention window
// and never reaches past it. AllProjects reads every project partition
// instead of this store's own; account events are always read.
type Query struct {
	Since       time.Time
	AllProjects bool
}

// The work of one Read is bounded: segmentLimit is the most bytes taken from
// one segment, both from the file and out of gzip; readLimit is the most
// taken by one Read; dirLimit is the most entries listed from a directory.
// Tests lower them.
var (
	segmentLimit int64 = 8 << 20
	readLimit    int64 = 128 << 20
	dirLimit           = 4096
)

const daySeconds = 24 * 60 * 60

// errRead is the closed error of a Read that failed for another reason
// than an unsafe store or a denied permission.
var errRead = errors.New("telemetry: read failed")

// segmentName is a plain or compressed segment in a partition.
var segmentName = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2})\.ndjson(\.gz)?$`)

// Read returns the events of the account partition and of this store's
// project partition, or of every project with AllProjects, ordered by t. With
// project events off, this project's partition is the one the salt on disk
// names, if there is a salt.
// A record is returned only when it is a complete line of a known version
// and type that decodes into its typed struct with no other field, passes
// the checks of Encode again, sits in the partition its type belongs to,
// and has a t no older than retention and no more than a day after now. A
// string outside its closed set reads as other. Read filters by t only, not
// by the day a segment is named for; when a day has both a plain and a
// compressed segment, only the compressed one is read.
//
// Read writes nothing and starts no maintenance. Each partition is read
// under its lock taken shared, so an append or a prune is never seen half
// done. A store with no root on disk, or a closed one, reads nothing.
func (s *Store) Read(q Query) ([]Event, error) {
	now := s.o.Now().UTC().Unix()
	from := max(now-int64(s.o.RetentionDays)*daySeconds, q.Since.Unix())
	rd := reader{names: s.o.Names, from: from, to: now + daySeconds, budget: readLimit}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil, nil
	}
	if _, _, err := inspectRoot(s.path); err != nil {
		return nil, readCategory(err)
	}
	if err := sameDir(s.root, s.path); err != nil {
		return nil, readCategory(err)
	}
	rd.root, rd.path = s.root, s.path
	partitions := []string{accountPartition}
	if q.AllProjects {
		projects, err := projectPartitions(s.root)
		if err != nil {
			return nil, readCategory(err)
		}
		partitions = append(partitions, projects...)
	} else if s.project != "" {
		partitions = append(partitions, s.project)
	} else if id, ok, err := s.storedProject(); err == nil && ok {
		// With project events off the id comes from the salt on disk, read
		// only: no salt is created, so a store that never had one reads none.
		partitions = append(partitions, id)
	}
	for _, p := range partitions {
		rd.partition(p)
	}
	slices.SortStableFunc(rd.events, func(a, b stamped) int { return cmp.Compare(a.t, b.t) })
	events := make([]Event, len(rd.events))
	for i, e := range rd.events {
		events[i] = e.event
	}
	return events, nil
}

type stamped struct {
	t     int64
	event Event
}

// reader holds the bounds of one Read and what it has found.
type reader struct {
	root     *os.Root
	path     string
	names    Names
	from, to int64 // t bounds, both included
	budget   int64 // bytes left to take
	events   []stamped
}

// projectPartitions lists the project partitions under v1, sorted. Entries
// that are not directories or not named by a project id are left out.
func projectPartitions(r *os.Root) ([]string, error) {
	entries, err := listDir(r, "v1")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != accountPartition && partitionPattern.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

// partition reads the segments of one partition, oldest day first. A
// partition that is missing or fails a check is skipped.
func (rd *reader) partition(p string) {
	dir := "v1/" + p
	unlock, err := rd.lock(dir + "/" + lockName)
	if err != nil {
		return
	}
	defer unlock()
	entries, err := listDir(rd.root, dir)
	if err != nil {
		return
	}
	// A crash while compressing leaves both files; the .gz is complete,
	// since it is synced before its rename.
	chosen := map[string]string{}
	for _, e := range entries {
		m := segmentName.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() || !validDay(m[1]) {
			continue
		}
		if m[2] != "" || chosen[m[1]] == "" {
			chosen[m[1]] = e.Name()
		}
	}
	days := make([]string, 0, len(chosen))
	for d := range chosen {
		days = append(days, d)
	}
	slices.Sort(days)
	account := p == accountPartition
	for _, d := range days {
		name := chosen[d]
		rd.segment(dir+"/"+name, filepath.Ext(name) == ".gz", account)
	}
}

// lock takes the partition's lock shared, checked like every other file. A
// partition with no lock file was never written by a store and is read
// unlocked; Read never creates one. A lock taken on a file that left the
// name meanwhile, as a prune or purge removes the lock of a partition it
// empties, is let go and taken once more on what is now there.
func (rd *reader) lock(name string) (func(), error) {
	unlock, err := rd.lockOnce(name)
	if errors.Is(err, errMoved) {
		unlock, err = rd.lockOnce(name)
	}
	return unlock, err
}

// lockOnce takes the lock and confirms after locking that the locked file is
// the one checked and is still at name, as lockFile does.
func (rd *reader) lockOnce(name string) (func(), error) {
	f, err := openReading(rd.root, name)
	if errors.Is(err, fs.ErrNotExist) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	checked, err := fileInfo(f)
	if err != nil {
		return nil, scrub(err)
	}
	l := flock.New(filepath.Join(rd.path, filepath.FromSlash(name)), flock.SetFlag(os.O_RDONLY))
	if err := l.RLock(); err != nil {
		l.Close()
		return nil, scrub(err)
	}
	locked, err := l.Stat()
	if err != nil || !os.SameFile(locked, unwrap(checked)) {
		l.Close()
		return nil, errUnsafe
	}
	race("locked")
	at, err := rd.root.Lstat(name)
	if err != nil || !regular(at) || !os.SameFile(at, locked) {
		l.Close()
		return nil, errMoved
	}
	return func() { l.Close() }, nil
}

// segment reads one segment within the segment and Read limits. A segment
// that fails a check, or a gzip stream that breaks, ends with the last
// complete line before it.
func (rd *reader) segment(name string, compressed, account bool) {
	limit := min(segmentLimit, rd.budget)
	if limit <= 0 {
		return
	}
	f, err := openReading(rd.root, name)
	if err != nil {
		return
	}
	defer f.Close()
	src := io.LimitReader(f, limit)
	if compressed {
		z, err := gzip.NewReader(src)
		if err != nil {
			return
		}
		defer z.Close()
		src = z
	}
	taken := &io.LimitedReader{R: src, N: limit}
	rd.lines(taken, account)
	rd.budget -= limit - taken.N
}

// lines keeps the events of the complete lines of src. The buffer holds one
// line of at most maxLine bytes; a longer line is skipped without being
// held, and a last line without its newline is an interrupted write.
func (rd *reader) lines(src io.Reader, account bool) {
	br := bufio.NewReaderSize(src, maxLine)
	for {
		line, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			if err != nil {
				return
			}
			continue
		}
		if err != nil {
			return
		}
		e, t, ok := decodeRecord(line, rd.names)
		if !ok || t < rd.from || t > rd.to {
			continue
		}
		if isAccount, _ := accountEvent(e); isAccount != account {
			continue
		}
		rd.events = append(rd.events, stamped{t: t, event: e})
	}
}

// decoded is a record type Read can return.
type decoded interface {
	Event
	cleaner
	header() Common
}

func (c Common) header() Common { return c }

// decodeRecord decodes one complete line into its typed event and runs the
// checks of Encode on it; ok is false for anything else.
func decodeRecord(line []byte, names Names) (e Event, t int64, ok bool) {
	var head struct {
		V    int    `json:"v"`
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &head) != nil || head.V != schemaVersion {
		return nil, 0, false
	}
	switch head.Type {
	case "run":
		return decodeAs[Run](line, names, head.Type)
	case "quota":
		return decodeAs[Quota](line, names, head.Type)
	case "route":
		return decodeAs[Route](line, names, head.Type)
	case "gate":
		return decodeAs[Gate](line, names, head.Type)
	case "peer":
		return decodeAs[Peer](line, names, head.Type)
	case "reset":
		return decodeAs[Reset](line, names, head.Type)
	}
	return nil, 0, false
}

func decodeAs[E decoded](line []byte, names Names, typ string) (Event, int64, bool) {
	var e E
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if dec.Decode(&e) != nil || dec.Decode(new(json.RawMessage)) != io.EOF {
		return nil, 0, false
	}
	// JSON keys match without case, so the values are checked again.
	c := e.header()
	if c.V != schemaVersion || c.Type != typ {
		return nil, 0, false
	}
	value, err := e.clean(Common{V: schemaVersion, T: c.T}, names)
	if err != nil {
		return nil, 0, false
	}
	event, ok := value.(Event)
	return event, c.T, ok
}

// readCategory maps a failed check of the root to a closed error.
func readCategory(err error) error {
	var refused *refusedError
	switch {
	case errors.Is(err, errUnsafe), errors.Is(err, errOwner), errors.As(err, &refused):
		return errUnsafeStore
	case errors.Is(err, fs.ErrPermission):
		return errDenied
	}
	return errRead
}

// listDir lists at most dirLimit entries of a directory under r, checked
// like the directories of openReading and confirmed after the open.
func listDir(r *os.Root, dir string) ([]fs.DirEntry, error) {
	if err := secureDirs(r, dir, false, false); err != nil {
		return nil, err
	}
	f, err := r.Open(dir)
	if err != nil {
		return nil, scrub(err)
	}
	defer f.Close()
	opened, err := fileInfo(f)
	if err != nil {
		return nil, scrub(err)
	}
	now, err := r.Lstat(dir)
	if err != nil || linked(now) || !opened.IsDir() || !os.SameFile(unwrap(opened), now) {
		return nil, errUnsafe
	}
	if err := ownedFile(f, opened); err != nil {
		return nil, err
	}
	entries, err := f.ReadDir(dirLimit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, scrub(err)
	}
	return entries, nil
}

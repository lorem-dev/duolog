package bus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lorem-dev/duolog/internal/store"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/core/config"
)

var ErrTooLong = errors.New("message too long; use duolog put for larger content")

type Message struct {
	N    int64  `json:"n"`
	T    string `json:"t"`
	From string `json:"from"`
	To   string `json:"to"`
	K    string `json:"k"`
	Th   string `json:"th,omitempty"`
	Ref  string `json:"ref,omitempty"`
	S    string `json:"s"`
	P    bool   `json:"p,omitempty"`
	// W is how many seconds the sender of an ask waits for the reply.
	W int `json:"w,omitempty"`
}

type Bus struct {
	dir  string
	wire config.Wire
	// within, when set, bounds every wait for the bus lock: see Within.
	within context.Context
}

// Within returns the bus with every wait for its lock bounded by ctx: an
// operation tries the lock again every few milliseconds and fails with ctx's
// error once ctx ends. Best-effort notices use it, so that a process that
// stalls with the lock cannot hold back a hook.
func (b *Bus) Within(ctx context.Context) *Bus {
	c := *b
	c.within = ctx
	return &c
}

// Directory is the shared state directory containing this bus and its registry.
func (b *Bus) Directory() string { return b.dir }

func Open(dir string, w config.Wire) (*Bus, error) {
	if w.MaxSummaryBytes <= 0 || w.MaxLineBytes <= 0 {
		return nil, fmt.Errorf("wire byte limits must be positive")
	}
	if w.MaxSummaryBytes > 200 || w.MaxLineBytes > 512 {
		return nil, fmt.Errorf("wire byte limits must not exceed 200 summary bytes and 512 line bytes")
	}
	if err := os.MkdirAll(filepath.Join(dir, "cursors"), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "bus.ndjson"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &Bus{dir: dir, wire: w}, nil
}

// Each operation uses a separate lock handle, including calls on the same Bus.
// Inbox also holds the lock so cursor updates and tail repairs cannot race it.
func (b *Bus) lock() (*flock.Flock, error) {
	lock := flock.New(filepath.Join(b.dir, "bus.lock"))
	if err := store.Lock(b.within, lock); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lock, nil
}

func (b *Bus) Send(m Message) (int64, error) {
	if len(m.S) > b.wire.MaxSummaryBytes {
		return 0, ErrTooLong
	}
	lock, err := b.lock()
	if err != nil {
		return 0, err
	}
	defer lock.Close()
	f, err := os.OpenFile(filepath.Join(b.dir, "bus.ndjson"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n, err := repairTail(f)
	if err != nil {
		return 0, err
	}
	if n == math.MaxInt64 {
		return 0, fmt.Errorf("bus sequence exhausted")
	}
	m.N = n + 1
	m.T = time.Now().UTC().Format(time.RFC3339Nano)
	var line bytes.Buffer
	encoder := json.NewEncoder(&line)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(m); err != nil {
		return 0, err
	}
	if line.Len() > b.wire.MaxLineBytes {
		return 0, ErrTooLong
	}
	// Windows append-only handles cannot truncate a torn tail. Use a writable
	// handle and seek under bus.lock, which serializes repair and append.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return 0, err
	}
	written, err := f.Write(line.Bytes())
	if err != nil {
		return 0, err
	}
	if written != line.Len() {
		return 0, io.ErrShortWrite
	}
	return m.N, nil
}

// repairTail discards an unfinished write and reads only the last complete line.
// The caller must hold bus.lock throughout repair and the following append.
func repairTail(f *os.File) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	last, err := previousNewline(f, info.Size())
	if err != nil {
		return 0, err
	}
	end := last + 1
	if end != info.Size() {
		if err := f.Truncate(end); err != nil {
			return 0, err
		}
	}
	if end == 0 {
		return 0, nil
	}
	previous, err := previousNewline(f, last)
	if err != nil {
		return 0, err
	}
	line := make([]byte, last-previous-1)
	if _, err := f.ReadAt(line, previous+1); err != nil {
		return 0, err
	}
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		return 0, fmt.Errorf("last bus line: %w", err)
	}
	if m.N < 1 {
		return 0, fmt.Errorf("last bus line has invalid sequence %d", m.N)
	}
	return m.N, nil
}

// previousNewline searches backward before end in bounded blocks.
func previousNewline(f *os.File, end int64) (int64, error) {
	var block [4096]byte
	for end > 0 {
		start := max(int64(0), end-int64(len(block)))
		data := block[:end-start]
		if _, err := f.ReadAt(data, start); err != nil {
			return 0, err
		}
		if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
			return start + int64(i), nil
		}
		end = start
	}
	return -1, nil
}

// Inbox returns stored lines, including their newline, without re-encoding JSON.
func (b *Bus) Inbox(reader string) ([][]byte, error) {
	lines, _, err := b.InboxLimited(reader, 0)
	return lines, err
}

// InboxLimited consumes at most limit matching lines. Remaining messages stay
// unread for duolog inbox. A nonpositive limit reads the entire inbox.
func (b *Bus) InboxLimited(reader string, limit int) ([][]byte, bool, error) {
	return b.read(reader, limit, nil)
}

// Consume moves the reader's cursor past every unread message and calls take
// with the lines an earlier Consume kept and the matching unread lines. take
// returns the lines to keep: they replace the kept lines and come back in the
// reader's next Inbox, ahead of newer messages. The bus file itself is not
// changed. With no kept and no unread lines take is not called.
func (b *Bus) Consume(reader string, take func(held, unread [][]byte) (keep [][]byte)) error {
	_, _, err := b.read(reader, 0, take)
	return err
}

func (b *Bus) heldPath(reader string) string {
	return filepath.Join(b.dir, "held", url.PathEscape(reader))
}

// read is InboxLimited and Consume. A plain read of the whole inbox (no limit,
// no take) also returns, and clears, the lines an earlier Consume kept.
func (b *Bus) read(reader string, limit int, take func(held, unread [][]byte) [][]byte) ([][]byte, bool, error) {
	if reader == "" || reader == "." || reader == ".." {
		return nil, false, fmt.Errorf("invalid reader address %q", reader)
	}
	lock, err := b.lock()
	if err != nil {
		return nil, false, err
	}
	defer lock.Close()
	cursor := filepath.Join(b.dir, "cursors", url.PathEscape(reader))
	lines, more, offset, err := b.scan(reader, limit, cursor)
	if err != nil {
		return nil, false, err
	}
	whole := take == nil && limit <= 0
	if take != nil {
		err = b.hold(reader, take, lines)
		lines = nil
	} else if whole {
		lines, err = b.withHeld(reader, lines)
	}
	if err == nil {
		err = writeCursor(cursor, offset)
	}
	if err == nil && whole {
		// The cursor is written first: a failure here only repeats held lines.
		if err = os.Remove(b.heldPath(reader)); os.IsNotExist(err) {
			err = nil
		}
	}
	if err != nil {
		return nil, false, err
	}
	return lines, more, nil
}

// withHeld puts the lines a Consume kept ahead of lines.
func (b *Bus) withHeld(reader string, lines [][]byte) ([][]byte, error) {
	old, err := os.ReadFile(b.heldPath(reader))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return append(splitLines(old), lines...), nil
}

// hold gives take the kept and the unread lines and keeps the ones it returns
// in their place. The caller writes the cursor after it: a failure there only
// repeats lines.
func (b *Bus) hold(reader string, take func(held, unread [][]byte) [][]byte, lines [][]byte) error {
	path := b.heldPath(reader)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	held := splitLines(data)
	if len(held) == 0 && len(lines) == 0 {
		return nil
	}
	keep := take(held, lines)
	if len(keep) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeHeld(path, keep)
}

// openAt opens the bus file at the reader's cursor.
func (b *Bus) openAt(reader, cursor string) (*os.File, int64, error) {
	var offset int64
	data, err := os.ReadFile(cursor)
	if err == nil {
		offset, err = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil || offset < 0 {
			return nil, 0, fmt.Errorf("invalid cursor for %q: %q", reader, data)
		}
	} else if !os.IsNotExist(err) {
		return nil, 0, err
	}
	f, err := os.Open(filepath.Join(b.dir, "bus.ndjson"))
	if err != nil {
		return nil, 0, err
	}
	// A cursor past the end means someone replaced the file. Waiting for it to grow
	// would hide every new message until then.
	if info, err := f.Stat(); err != nil {
		_ = f.Close()
		return nil, 0, err
	} else if offset > info.Size() {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, offset, nil
}

// scan reads the matching lines after the reader's cursor, at most limit of
// them when limit is positive, and returns the offset to move the cursor to.
func (b *Bus) scan(reader string, limit int, cursor string) (lines [][]byte, more bool, offset int64, err error) {
	f, offset, err := b.openAt(reader, cursor)
	if err != nil {
		return nil, false, 0, err
	}
	defer f.Close()
	input := bufio.NewReader(f)
	for {
		line, err := input.ReadBytes('\n')
		if err == io.EOF {
			break // The incomplete tail is not consumed, even if it is valid JSON.
		}
		if err != nil {
			return nil, false, 0, err
		}
		var address struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(line, &address); err != nil {
			return nil, false, 0, fmt.Errorf("bus line at %d: %w", offset, err)
		}
		matches := address.From != reader && (address.To == reader || address.To == "*")
		if matches && limit > 0 && len(lines) >= limit {
			more = true
		}
		if !more {
			offset += int64(len(line))
			if matches {
				lines = append(lines, line)
			}
		}
	}
	return lines, more, offset, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, data[:i+1])
		data = data[i+1:]
	}
	return lines
}

// writeHeld replaces the held file with lines in one rename.
func writeHeld(path string, lines [][]byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".held-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(bytes.Join(lines, nil))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return store.Rename(f.Name(), path)
}

func writeCursor(path string, offset int64) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".cursor-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.WriteString(strconv.FormatInt(offset, 10) + "\n")
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return store.Rename(f.Name(), path)
}

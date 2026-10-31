package bus

import (
	"bufio"
	"bytes"
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
	"github.com/lorem-dev/duolog/internal/config"
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
}

type Bus struct {
	dir  string
	wire config.Wire
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
	if err := lock.Lock(); err != nil {
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
	if reader == "" || reader == "." || reader == ".." {
		return nil, false, fmt.Errorf("invalid reader address %q", reader)
	}
	lock, err := b.lock()
	if err != nil {
		return nil, false, err
	}
	defer lock.Close()
	cursor := filepath.Join(b.dir, "cursors", url.PathEscape(reader))
	data, err := os.ReadFile(cursor)
	var offset int64
	if err == nil {
		offset, err = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
		if err != nil || offset < 0 {
			return nil, false, fmt.Errorf("invalid cursor for %q: %q", reader, data)
		}
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}
	f, err := os.Open(filepath.Join(b.dir, "bus.ndjson"))
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	// A cursor past the end means someone replaced the file. Waiting for it to grow
	// would hide every new message until then.
	if info, err := f.Stat(); err != nil {
		return nil, false, err
	} else if offset > info.Size() {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, false, err
	}
	input := bufio.NewReader(f)
	var lines [][]byte
	var more bool
	for {
		line, err := input.ReadBytes('\n')
		if err == io.EOF {
			break // The incomplete tail is not consumed, even if it is valid JSON.
		}
		if err != nil {
			return nil, false, err
		}
		var address struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(line, &address); err != nil {
			return nil, false, fmt.Errorf("bus line at %d: %w", offset, err)
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
	if err := writeCursor(cursor, offset); err != nil {
		return nil, false, err
	}
	return lines, more, nil
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

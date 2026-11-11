package delegate

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"

	"github.com/lorem-dev/duolog/internal/core/runview"
)

const (
	// TailStart is how far before the end of a log runs tail starts when it
	// is given no offset.
	TailStart = 64 << 10
	// TailMaxLines is the most lines one page of runs tail holds.
	TailMaxLines = 200
	// TailMaxBytes is the most bytes of a log one call of runs tail reads, so
	// that a call stays short however long the log or one of its lines is.
	TailMaxBytes = 1 << 20
)

// LogReader reads a worker's log that grows, whole lines at a time, and
// renders them (runview). It opens the file for each read, so it holds no
// file between reads.
type LogReader struct {
	path   string
	offset int64
	view   runview.Renderer
	opened bool
	// cutting says that offset is inside a line longer than TailMaxBytes
	// that began at cutFrom: it is skipped through its newline and printed
	// as one line that gives its size.
	cutting bool
	cutFrom int64
}

// Chunk is what one Read gave: the rendered lines, whether the log started
// over, and whether the read reached the end of the log.
type Chunk struct {
	Lines []string
	Reset bool
	AtEnd bool
}

// Offset is the byte offset after the last line read; inside a line that is
// cut, it is how far the cut has read.
func (l *LogReader) Offset() int64 { return l.offset }

// Read renders the lines that came since the last read. A line without its
// newline waits for it, unless last says the log has ended. A new path, or a
// file shorter than the offset, starts over from byte 0 (Reset). With
// maxLines above 0 it reads at most TailMaxBytes and stops before the log
// line whose lines would pass maxLines, except the first, which is cut to
// maxLines; with 0 it reads to the end. A missing file reads as an empty
// log.
func (l *LogReader) Read(path string, last bool, maxLines int) (Chunk, error) {
	if l.path != path {
		*l = LogReader{path: path, opened: l.opened}
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Chunk{AtEnd: true}, nil
	}
	if err != nil {
		return Chunk{}, err
	}
	defer f.Close()
	l.opened = true
	info, err := f.Stat()
	if err != nil {
		return Chunk{}, err
	}
	var c Chunk
	size := info.Size()
	if size < l.offset {
		// The log started over.
		*l = LogReader{path: path, opened: true}
		c.Reset = true
	}
	for {
		before := l.offset
		if err := l.readBlock(f, size, last, maxLines, &c); err != nil {
			return c, err
		}
		if maxLines > 0 || l.offset == before || l.offset >= size {
			break
		}
	}
	c.AtEnd = l.offset >= size
	return c, nil
}

// readBlock reads at most TailMaxBytes from the offset, up to size, and
// takes the lines in it.
func (l *LogReader) readBlock(f *os.File, size int64, last bool, maxLines int, c *Chunk) error {
	buf := make([]byte, min(size-l.offset, TailMaxBytes))
	if len(buf) == 0 {
		return nil
	}
	n, err := f.ReadAt(buf, l.offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	buf = buf[:n]
	start := l.offset
	end := start + int64(n)
	atEnd := last && end == size
	pos, ok := l.skipCut(c, buf, atEnd, maxLines)
	if !ok {
		return nil
	}
	for {
		i := bytes.IndexByte(buf[pos:], '\n')
		if i < 0 {
			break
		}
		if !take(c, l.view.Render(buf[pos:pos+i]), maxLines) {
			return nil
		}
		pos += i + 1
		l.offset = start + int64(pos)
	}
	l.rest(c, buf[pos:], pos == 0 && n == TailMaxBytes, atEnd, maxLines)
	return nil
}

// rest takes the bytes after the last newline of a block, which end at the
// end of the block: a line that fills the whole block is too long to render
// and is cut; at the end of an ended log the last line is taken without its
// newline; otherwise the line waits for the next read.
func (l *LogReader) rest(c *Chunk, rest []byte, whole, atEnd bool, maxLines int) {
	end := l.offset + int64(len(rest))
	switch {
	case len(rest) == 0:
	case whole:
		// The line is never rendered, so a Codex item it completes is
		// closed now.
		if id := completedItem(rest); id != "" {
			l.view.Close(id)
		}
		l.cutting, l.cutFrom = true, l.offset
		if atEnd && !l.endCut(c, end, maxLines) {
			l.cutting = false
			return
		}
		l.offset = end
	case atEnd:
		if take(c, l.view.Render(rest), maxLines) {
			l.offset = end
		}
	}
}

// skipCut skips the rest of a line being cut in buf, read at the offset,
// and takes its cut line when it ends. It returns where the next line starts
// in buf, and false when nothing more of buf is to be read now.
func (l *LogReader) skipCut(c *Chunk, buf []byte, atEnd bool, maxLines int) (int, bool) {
	if !l.cutting {
		return 0, true
	}
	end := l.offset + int64(len(buf))
	i := bytes.IndexByte(buf, '\n')
	if i < 0 {
		if !atEnd || l.endCut(c, end, maxLines) {
			l.offset = end
		}
		return 0, false
	}
	if !l.endCut(c, l.offset+int64(i), maxLines) {
		return 0, false
	}
	l.offset += int64(i) + 1
	return i + 1, true
}

// cutHead is how much of the start of a cut line completedItem reads.
const cutHead = 4 << 10

var (
	completedType = regexp.MustCompile(`^\s*\{\s*"type"\s*:\s*"item\.completed"`)
	itemID        = regexp.MustCompile(`"item"\s*:\s*\{[^{}]*?"id"\s*:\s*"([^"\\]{1,128})"`)
)

// completedItem is the id of the Codex item that a log line completes, read
// from the first bytes of the line only; "" when the line is not a Codex
// completion or names no id there.
func completedItem(line []byte) string {
	line = line[:min(len(line), cutHead)]
	if !completedType.Match(line) {
		return ""
	}
	if m := itemID.FindSubmatch(line); m != nil {
		return string(m[1])
	}
	return ""
}

// endCut takes the one line that stands for the cut line ending at end.
func (l *LogReader) endCut(c *Chunk, end int64, maxLines int) bool {
	line := fmt.Sprintf("> (output of %d MiB not shown)", (end-l.cutFrom)>>20)
	if !take(c, []string{line}, maxLines) {
		return false
	}
	l.cutting = false
	return true
}

// take adds the lines of one log line to c, unless they would pass maxLines
// (0: no limit); the first log line of c is cut to maxLines instead.
func take(c *Chunk, lines []string, maxLines int) bool {
	if maxLines > 0 && len(c.Lines)+len(lines) > maxLines {
		if len(c.Lines) > 0 {
			return false
		}
		lines = lines[:maxLines]
	}
	c.Lines = append(c.Lines, lines...)
	return true
}

// TailPage is one page of a run's log for runs tail. The caller passes
// Offset and Cursor back unchanged to read the next page.
type TailPage struct {
	Offset int64    `json:"offset"`
	Reset  bool     `json:"reset"`
	Lines  []string `json:"lines"`
	Ended  bool     `json:"ended"`
	State  string   `json:"state"`
	Cursor string   `json:"cursor"`
}

// tailCursor is what a cursor holds: the log it was read from, the Codex
// items open in it, and the start of a line being cut.
type tailCursor struct {
	Log  string   `json:"log"`
	Open []string `json:"open"`
	Cut  *int64   `json:"cut,omitempty"`
}

// Tail reads one page of rec's log from the byte offset from with the
// cursor of the page before: at most TailMaxLines lines and TailMaxBytes
// bytes. A negative from starts in the last TailStart bytes, at a line. A
// cursor of another log, one that does not decode, or a from past the end
// reads the log from byte 0 (Reset). The page has Ended when the run is not
// running and the page reached the end of the log.
func Tail(rec Record, from int64, cursor string) (TailPage, error) {
	page := TailPage{Lines: []string{}, State: rec.State}
	ended := rec.State != "running"
	path := rec.LogPath()
	if path == "" {
		page.Ended = ended
		return page, nil
	}
	l, reset, err := tailReader(rec, path, from, cursor)
	if err != nil {
		return page, err
	}
	c, err := l.Read(path, ended, TailMaxLines)
	if err != nil {
		return page, err
	}
	if c.Lines != nil {
		page.Lines = c.Lines
	}
	page.Offset, page.Reset, page.Ended = l.Offset(), reset || c.Reset, ended && c.AtEnd
	if l.opened {
		page.Cursor = encodeCursor(rec.Log, l)
	}
	return page, nil
}

// tailReader is the LogReader at the start of a page, and whether the page
// starts over.
func tailReader(rec Record, path string, from int64, cursor string) (*LogReader, bool, error) {
	l := &LogReader{path: path}
	cur, ok := decodeCursor(cursor)
	reset := cursor != "" && (!ok || cur.Log != logKey(rec.Log))
	size, err := logSize(path)
	if err != nil {
		return nil, false, err
	}
	if reset || from > size {
		return l, true, nil
	}
	l.view = runview.NewRenderer(cur.Open)
	if cur.Cut != nil && *cur.Cut <= from {
		l.cutting, l.cutFrom = true, *cur.Cut
	}
	if from >= 0 {
		l.offset = from
		return l, false, nil
	}
	l.offset, err = tailStart(path, size)
	return l, false, err
}

// logSize is the size of the log at path, 0 when it does not exist yet.
func logSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// tailStart is where a page without an offset starts: TailStart bytes before
// the end, after the first newline there; the end when there is none.
func tailStart(path string, size int64) (int64, error) {
	start := size - TailStart
	if start <= 0 {
		return 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, size-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if i := bytes.IndexByte(buf[:n], '\n'); i >= 0 {
		return start + int64(i) + 1, nil
	}
	return size, nil
}

// logKey names a record's log in a cursor: the first 8 hex digits of its
// SHA-256.
func logKey(log string) string {
	sum := sha256.Sum256([]byte(log))
	return hex.EncodeToString(sum[:4])
}

// encodeCursor is the cursor after l has read log: base64url JSON.
func encodeCursor(log string, l *LogReader) string {
	cur := tailCursor{Log: logKey(log), Open: l.view.Open()}
	if cur.Open == nil {
		cur.Open = []string{}
	}
	if l.cutting {
		cur.Cut = &l.cutFrom
	}
	data, err := json.Marshal(cur)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

// decodeCursor reads a cursor; false when it does not decode. An empty
// cursor decodes to none.
func decodeCursor(cursor string) (tailCursor, bool) {
	var cur tailCursor
	if cursor == "" {
		return cur, true
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || json.Unmarshal(data, &cur) != nil {
		return tailCursor{}, false
	}
	return cur, true
}

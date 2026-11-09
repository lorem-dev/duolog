package delegate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/core/runview"
)

// maxEndRunes is the longest result line Watch prints when a run ends.
const maxEndRunes = 200

// LogPath is the path of the record's worker log, or "" before the worker
// started.
func (r Record) LogPath() string {
	switch {
	case r.Log == "":
		return ""
	case filepath.IsAbs(r.Log):
		return r.Log
	}
	return filepath.Join(r.WorkRoot, filepath.FromSlash(r.Log))
}

// Target is "agent/model", or "" while the worker has not started.
func (r Record) Target() string {
	if r.Agent == "" {
		return ""
	}
	if r.Model == "" {
		return r.Agent
	}
	return r.Agent + "/" + r.Model
}

// Watch prints a run for a person until it ends or ctx is done: a header,
// then what the worker said and one line per tool call (runview), from the
// start of its log and on as the log grows, then the run's end. The record at
// recordPath says when the run ended; alive is as in ReadRecord. A log that
// starts over, as a retry does, is read again from its start. A cancelled
// ctx ends the watch without an error. poll is the wait between reads.
func Watch(ctx context.Context, w io.Writer, recordPath string, alive func(pid int) bool, poll time.Duration) error {
	rec, err := ReadRecord(recordPath, alive)
	if err != nil {
		return err
	}
	header := strings.Join(strings.Fields(rec.ID+" "+rec.Role+" "+rec.Target()+" "+filepath.Base(rec.WorkRoot)), " ")
	if _, err := fmt.Fprintln(w, header); err != nil {
		return err
	}
	f := &logFollower{w: w}
	defer f.close()
	for {
		// The record is read before the log, so every line written before the
		// run ended is read.
		ended := rec.State != "running"
		if path := rec.LogPath(); path != "" {
			if err := f.drain(path, ended); err != nil {
				return err
			}
		}
		if ended {
			return finish(w, rec, f.opened)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
		if rec, err = ReadRecord(recordPath, alive); err != nil {
			return err
		}
	}
}

// finish prints the run's last line.
func finish(w io.Writer, rec Record, logged bool) error {
	if !logged {
		if _, err := fmt.Fprintln(w, "no log: the worker never started"); err != nil {
			return err
		}
	}
	line := "ended: " + rec.State
	if text := strings.Join(strings.Fields(runview.Clean(rec.Line)), " "); text != "" {
		if utf8.RuneCountInString(text) > maxEndRunes {
			text = string([]rune(text)[:maxEndRunes-3]) + "..."
		}
		line += ": " + text
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

// logFollower reads a log that grows, whole lines at a time.
type logFollower struct {
	w       io.Writer
	file    *os.File
	path    string
	offset  int64
	partial []byte
	view    runview.Renderer
	opened  bool
}

func (f *logFollower) close() {
	if f.file != nil {
		_ = f.file.Close()
	}
}

// drain prints the lines that came since the last call. A line without its
// newline waits for it, unless last says the log has ended.
func (f *logFollower) drain(path string, last bool) error {
	if f.path != path {
		f.close()
		f.file, f.path, f.offset, f.partial, f.view = nil, path, 0, nil, runview.Renderer{}
	}
	if f.file == nil {
		file, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		f.file, f.opened = file, true
	}
	if info, err := f.file.Stat(); err == nil && info.Size() < f.offset {
		// The log started over.
		if _, err := f.file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		f.offset, f.partial, f.view = 0, nil, runview.Renderer{}
	}
	data, err := io.ReadAll(f.file)
	if err != nil {
		return err
	}
	f.offset += int64(len(data))
	data = append(f.partial, data...)
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		if err := f.print(data[:i]); err != nil {
			return err
		}
		data = data[i+1:]
	}
	f.partial = append([]byte(nil), data...)
	if last && len(f.partial) > 0 {
		err = f.print(f.partial)
		f.partial = nil
	}
	return err
}

func (f *logFollower) print(line []byte) error {
	for _, text := range f.view.Render(line) {
		if _, err := fmt.Fprintln(f.w, text); err != nil {
			return err
		}
	}
	return nil
}

package delegate

import (
	"context"
	"fmt"
	"io"
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
	l := &LogReader{}
	for {
		// The record is read before the log, so every line written before the
		// run ended is read.
		ended := rec.State != "running"
		if path := rec.LogPath(); path != "" {
			if err := printChunk(w, l, path, ended); err != nil {
				return err
			}
		}
		if ended {
			return finish(w, rec, l.opened)
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

// printChunk prints the lines of path that came since l last read it. A line
// without its newline waits for it, unless last says the log has ended.
func printChunk(w io.Writer, l *LogReader, path string, last bool) error {
	c, err := l.Read(path, last, 0)
	if err != nil {
		return err
	}
	for _, text := range c.Lines {
		if _, err := fmt.Fprintln(w, text); err != nil {
			return err
		}
	}
	return nil
}

// Package journal keeps the learning-mode records under .duolog/journal/.
package journal

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/gofrs/flock"

	"github.com/lorem-dev/duolog/internal/core/record"
)

// The journal records live in core/record; these names keep their importers
// unchanged.
type (
	FileChange        = record.FileChange
	Delegation        = record.Delegation
	Task              = record.Task
	Check             = record.Check
	Score             = record.Score
	Verdict           = record.Verdict
	CalibrationResult = record.CalibrationResult
	OpenTask          = record.OpenTask
	Declined          = record.Declined
)

// A delegation with one of these outcomes says nothing about the quality of
// its entry, so aggregation and escalation skip it.
const (
	OutcomeInfra = record.OutcomeInfra
	OutcomeLead  = record.OutcomeLead
)

// CalibrationFile holds the check results of calibration cells and the tasks
// the person dropped from a run.
const CalibrationFile = "calibration.ndjson"

var Outcomes = map[string]float64{"accepted": 3, "small_fixes": 2, "reworked": 1, "dropped": 0}

type Journal struct{ Dir string }

func Open(stateDir string) (*Journal, error) {
	dir := filepath.Join(stateDir, "journal")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &Journal{Dir: dir}, nil
}

func (j *Journal) lock() (*flock.Flock, error) {
	l := flock.New(filepath.Join(j.Dir, "journal.lock"))
	if err := l.Lock(); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// Append writes one JSON line to the named record file.
func (j *Journal) Append(name string, record any) error {
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	l, err := j.lock()
	if err != nil {
		return err
	}
	defer l.Close()
	// No O_APPEND: on Windows it opens the file without the right to truncate
	// it. The lock keeps writers apart, so writing at the end is enough.
	f, err := os.OpenFile(filepath.Join(j.Dir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err := dropPartialTail(f); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// dropPartialTail repairs an interrupted last line (no final newline) so the
// next record starts on a clean line. A tail that is a complete JSON value is
// kept and gets its newline; anything else is cut back to the last newline.
func dropPartialTail(f *os.File) error {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	size := info.Size()
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, size-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	// Read backwards in chunks until the newline before the tail.
	var tail []byte
	start := size
	for start > 0 {
		n := int64(4096)
		if n > start {
			n = start
		}
		start -= n
		chunk := make([]byte, n)
		if _, err := f.ReadAt(chunk, start); err != nil {
			return err
		}
		tail = slices.Concat(chunk, tail)
		if i := bytes.LastIndexByte(chunk, '\n'); i >= 0 {
			tail = tail[i+1:]
			start += int64(i) + 1
			break
		}
	}
	if json.Valid(tail) {
		_, err := f.WriteAt([]byte{'\n'}, size)
		return err
	}
	return f.Truncate(start)
}

// ReadAll reads every record of a file. A missing file is empty and a broken
// last line (an interrupted write) is skipped; any earlier broken line errors.
func ReadAll[T any](j *Journal, name string) ([]T, error) {
	data, err := os.ReadFile(filepath.Join(j.Dir, name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lines [][]byte
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scan.Scan() {
		lines = append(lines, append([]byte(nil), scan.Bytes()...))
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	var out []T
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec T
		if err := json.Unmarshal(line, &rec); err != nil {
			if i == len(lines)-1 {
				break
			}
			return nil, fmt.Errorf("%s line %d: %w", name, i+1, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func NewID(prefix string, now time.Time) string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return prefix + "-" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

func (j *Journal) openState(fn func(map[string]OpenTask) (bool, error)) error {
	l, err := j.lock()
	if err != nil {
		return err
	}
	defer l.Close()
	path := filepath.Join(j.Dir, "open.json")
	state := map[string]OpenTask{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("open.json: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	write, err := fn(state)
	if err != nil || !write {
		return err
	}
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// StartTask opens a task for the lead; callable from delegate code as well.
func (j *Journal) StartTask(lead, title, base string, now time.Time) (OpenTask, error) {
	var task OpenTask
	err := j.openState(func(state map[string]OpenTask) (bool, error) {
		if cur, ok := state[lead]; ok {
			return false, fmt.Errorf("task %s is open; run duolog task finish first", cur.ID)
		}
		task = OpenTask{ID: NewID("t", now), Lead: lead, Title: title, Base: base, Started: now.UTC()}
		state[lead] = task
		return true, nil
	})
	return task, err
}

func (j *Journal) CurrentTask(lead string) (OpenTask, bool, error) {
	var task OpenTask
	var ok bool
	err := j.openState(func(state map[string]OpenTask) (bool, error) {
		task, ok = state[lead]
		return false, nil
	})
	return task, ok, err
}

func (j *Journal) CloseTask(lead string) (OpenTask, error) {
	var task OpenTask
	err := j.openState(func(state map[string]OpenTask) (bool, error) {
		cur, ok := state[lead]
		if !ok {
			return false, fmt.Errorf("lead %s has no open task", lead)
		}
		task = cur
		delete(state, lead)
		return true, nil
	})
	return task, err
}

func (j *Journal) TaskDir(task, delegation string) string {
	return filepath.Join(j.Dir, "tasks", task, delegation)
}

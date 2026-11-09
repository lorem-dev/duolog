package delegate

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/policy"
)

// Record is the result of one detached delegation. The server writes it with
// State "running" before it starts the run. From then on only the run writes
// it: its PID when it starts, and its outcome over it at the end.
type Record struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Lead     string `json:"lead"`
	TaskPath string `json:"task_path"` // relative to WorkRoot
	WorkRoot string `json:"work_root"`
	PID      int    `json:"pid,omitempty"`
	// Use, AskedNeeds, Force and SkipPreflight are the options the MCP call
	// started the run with; a second call that differs in one is another run.
	Use           string     `json:"use,omitempty"`
	AskedNeeds    []string   `json:"asked_needs,omitempty"`
	Force         bool       `json:"force,omitempty"`
	SkipPreflight bool       `json:"skip_preflight,omitempty"`
	State         string     `json:"state"` // running, done, needs, run_here, failed
	Line          string     `json:"line,omitempty"`
	RunHere       *Placement `json:"run_here,omitempty"`
	Needs         []string   `json:"needs,omitempty"`
	// Blocked is set when a preflight refused the run: the Check commands the
	// gate would not allow. Skipped holds the Check lines not read as a command,
	// on a refusal and on every run that judged them.
	Blocked []policy.Blocked `json:"blocked,omitempty"`
	Skipped []string         `json:"skipped_checks,omitempty"`
	Started time.Time        `json:"started"`
	Ended   time.Time        `json:"ended,omitzero"`
	// Done, Summary, Files and Decisions are the worker's answer whole, for the
	// MCP delegate tool; Line is the CLI's line, which cuts them to fit.
	Done      bool     `json:"done,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Files     []string `json:"files,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
}

// RecordDir is where records live: <state dir>/delegations.
func RecordDir(stateDir string) string { return filepath.Join(stateDir, "delegations") }

// WriteRecord writes r atomically (temp file, then rename) to path, so a
// reader sees the old file or the new one and never half of either.
func WriteRecord(path string, r Record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".record-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(append(data, '\n'))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// ReadRecord reads one record. A running record whose PID alive says is gone
// reads with State "lost". A nil alive counts every process as alive.
func ReadRecord(path string, alive func(pid int) bool) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, fmt.Errorf("delegation record %s: %w", filepath.Base(path), err)
	}
	if r.State == "running" && r.PID > 0 && alive != nil && !alive(r.PID) {
		r.State = "lost"
	}
	return r, nil
}

// ListRecords returns the records in dir, newest first. A missing folder holds
// none, and a file that is not a readable record is left out.
func ListRecords(dir string, alive func(pid int) bool) ([]Record, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		r, err := ReadRecord(filepath.Join(dir, e.Name()), alive)
		if err != nil {
			continue
		}
		records = append(records, r)
	}
	slices.SortFunc(records, func(a, b Record) int {
		return cmp.Or(b.Started.Compare(a.Started), strings.Compare(b.ID, a.ID))
	})
	return records, nil
}

// RecordFor is the record for outcome out and error err: the state the CLI's
// exit would give (needs on Needs, run_here on RunHere, failed on err).
func RecordFor(base Record, out Outcome, err error, now time.Time) Record {
	r := base
	r.Ended = now
	r.Line = out.Line
	switch {
	case err != nil:
		r.State, r.Line = "failed", err.Error()
		var pe *PreflightError
		if errors.As(err, &pe) {
			r.Blocked, r.Skipped = slices.Clone(pe.Blocked), slices.Clone(pe.Skipped)
		}
	case len(out.Needs) > 0:
		r.State, r.Needs = "needs", slices.Clone(out.Needs)
	case out.RunHere != nil:
		p := *out.RunHere
		r.State, r.RunHere = "run_here", &p
	default:
		r.State = "done"
	}
	if err == nil {
		res := out.Result
		r.Done, r.Summary = res.Done, res.Summary
		r.Files, r.Decisions = slices.Clone(res.Files), slices.Clone(res.Decisions)
		r.Skipped = slices.Clone(out.SkippedChecks)
	}
	return r
}

// ParsePlacement reads the line Placement.String writes.
func ParsePlacement(line string) (Placement, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "run here: ")
	if !ok {
		return Placement{}, false
	}
	if task, ok := strings.CutPrefix(rest, "self task="); ok {
		return Placement{Where: "self", Task: task}, task != ""
	}
	rest, ok = strings.CutPrefix(rest, "subagent model=")
	if !ok {
		return Placement{}, false
	}
	model, rest, ok := strings.Cut(rest, " effort=")
	if !ok {
		return Placement{}, false
	}
	effort, task, ok := strings.Cut(rest, " task=")
	if !ok || task == "" {
		return Placement{}, false
	}
	return Placement{Where: "subagent", Model: model, Effort: effort, Task: task}, true
}

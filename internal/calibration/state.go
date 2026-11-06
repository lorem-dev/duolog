package calibration

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/store"
)

// ProjectFile is the shared part under Dir; everything else there is personal.
const ProjectFile = "project.yaml"

func Dir(stateDir string) string { return filepath.Join(stateDir, "calibration") }

func RunsDir(stateDir string) string { return filepath.Join(Dir(stateDir), "runs") }

type CellState string

const (
	CellPending     CellState = "pending"
	CellRunning     CellState = "running"
	CellDone        CellState = "done"
	CellFailedLimit CellState = "failed_limit"
	CellInvalid     CellState = "invalid"
	CellSkipped     CellState = "skipped"
	CellLeakage     CellState = "leakage_exposed"
)

// CellSetupChanged is a cell whose candidate changed how a check is defined:
// its checks ran but are no pass or failure; the person decides in judge.
const CellSetupChanged CellState = "check_setup_changed"

// Cell is one task replayed on one entry.
type Cell struct {
	ID    string    `json:"id"`
	Task  string    `json:"task"`
	Class string    `json:"class"`
	Entry string    `json:"entry"`
	Round int       `json:"round"` // 1 or 2
	State CellState `json:"state"`
	// Passed is true when the hidden tests and the class checks all exit 0.
	Passed  bool    `json:"passed,omitempty"`
	Tokens  int64   `json:"tokens,omitempty"`
	USD     float64 `json:"usd,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
	// Weekly and Window are measured percentage points, 0 when unmeasured.
	Weekly float64 `json:"weekly,omitempty"`
	Window float64 `json:"window,omitempty"`
	Reason string  `json:"reason,omitempty"` // "time cap", "raced out", "provider limit", ...
	Tree   string  `json:"tree,omitempty"`   // temp tree while running
	// SetupChanged are the files of a check_setup_changed cell that define
	// how a check runs and that its candidate changed.
	SetupChanged []string `json:"setup_changed,omitempty"`
}

type WindowSpend struct {
	ResetsAt  time.Time `json:"resets_at"`
	StartUsed float64   `json:"start_used"`
	LastUsed  float64   `json:"last_used"`
}

type Spend struct {
	WeeklyPct map[string]float64     `json:"weekly_pct,omitempty"` // agent -> percentage points spent by this run
	Windows   map[string]WindowSpend `json:"windows,omitempty"`    // agent -> current five-hour window
	// Levels holds each window's last reading by agent/kind[/model], and
	// Outstanding, by the same key, the run's charges that reading may not
	// show yet. The lead reserve is kept against the two together.
	Levels      map[string]WindowSpend `json:"levels,omitempty"`
	Outstanding map[string]Outstanding `json:"outstanding,omitempty"`
	// JudgePct is the part of WeeklyPct that spends the plan's judge share:
	// judge calls and the estimates of planned rewrites.
	JudgePct map[string]float64 `json:"judge_pct,omitempty"`
}

type Approval struct {
	Hash string    `json:"hash"`
	Time time.Time `json:"time"`
}

// Run is the personal record of one calibration, kept in RunsDir/<id>/run.json.
type Run struct {
	ID       string    `json:"id"`
	Base     string    `json:"base,omitempty"`
	Step     string    `json:"step"` // survey, classes, mine, plan, run, judge, propose
	Created  time.Time `json:"created"`
	Seed     int64     `json:"seed"`
	Approved *Approval `json:"approved,omitempty"`
	Cells    []Cell    `json:"cells,omitempty"`
	Spend    Spend     `json:"spend"`
	Pause    time.Time `json:"pause,omitzero"`
	Stopped  string    `json:"stopped,omitempty"`
}

// NewRun saves a fresh run at the survey step with its own sampling seed.
func NewRun(stateDir string, now time.Time) (*Run, error) {
	r := &Run{
		ID:      journal.NewID("cal", now),
		Step:    "survey",
		Created: now.UTC(),
		Seed:    rand.Int64(),
	}
	if err := r.Save(stateDir); err != nil {
		return nil, err
	}
	return r, nil
}

func LoadRun(stateDir, id string) (*Run, error) {
	if id == "" || id != filepath.Base(id) || id == "." || id == ".." {
		return nil, fmt.Errorf("calibration run %q: not a run id", id)
	}
	data, err := os.ReadFile(filepath.Join(RunsDir(stateDir), id, "run.json"))
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("calibration run %s: %w", id, err)
	}
	if r.ID != id {
		return nil, fmt.Errorf("calibration run %s: run.json names %q", id, r.ID)
	}
	return &r, nil
}

// LatestRun returns the run created last; a folder without run.json is not a run.
func LatestRun(stateDir string) (*Run, bool, error) {
	entries, err := os.ReadDir(RunsDir(stateDir))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var latest *Run
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(RunsDir(stateDir), entry.Name(), "run.json")); os.IsNotExist(err) {
			continue
		}
		r, err := LoadRun(stateDir, entry.Name())
		if err != nil {
			return nil, false, err
		}
		if latest == nil || r.Created.After(latest.Created) || r.Created.Equal(latest.Created) && r.ID > latest.ID {
			latest = r
		}
	}
	return latest, latest != nil, nil
}

func (r *Run) Dir(stateDir string) string { return filepath.Join(RunsDir(stateDir), r.ID) }

// Save replaces run.json through a temporary file, so a reader never sees half
// a run and a failed write keeps the previous one.
func (r *Run) Save(stateDir string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	dir := r.Dir(stateDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".run-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(append(data, '\n'))
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		return err
	}
	return store.Rename(tmp.Name(), filepath.Join(dir, "run.json"))
}

// Lock keeps one calibration at a time per state root; it never waits.
func Lock(stateDir string) (unlock func(), err error) {
	if err := os.MkdirAll(Dir(stateDir), 0o750); err != nil {
		return nil, err
	}
	l := flock.New(filepath.Join(Dir(stateDir), "calibration.lock"))
	locked, err := l.TryLock()
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	if !locked {
		_ = l.Close()
		return nil, errors.New("calibration is running in another duolog process")
	}
	return func() { _ = l.Close() }, nil
}

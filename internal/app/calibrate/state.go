package calibration

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/store"
)

// ProjectFile is the shared part under Dir; everything else there is personal.
const ProjectFile = "project.yaml"

func Dir(stateDir string) string { return filepath.Join(stateDir, "calibration") }

func RunsDir(stateDir string) string { return filepath.Join(Dir(stateDir), "runs") }

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
	// WaitReason says what the run waits for while a step waits for a
	// session slot of the user's account, "" otherwise.
	WaitReason string `json:"wait_reason,omitempty"`
	// Receipts are the last step receipt of each cell, rewrite or judge
	// series, as step.attempt: each actual start takes a new one, which tags
	// its account reservation, so a resume can tell a step whose worker may
	// still run and never reuses one for an uncharged rerun.
	Receipts map[string]string `json:"receipts,omitempty"`
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
	release, locked, err := store.TryLockFile(filepath.Join(Dir(stateDir), "calibration.lock"))
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, errors.New("calibration is running in another duolog process")
	}
	return func() { _ = release() }, nil
}

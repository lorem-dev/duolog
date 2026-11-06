package calibration

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/journal"
)

// RecordResults appends the check result of each done cell of run to the
// journal's calibration file, once per run, task and entry: calling it again
// adds only the results it has not written yet. Cells that did not finish,
// were cut, or leaked the solution write no result. Each result names the
// ladder of the role its cell ran as: the class's step ladder for that role
// when it has one, else the class ladder.
func RecordResults(j *journal.Journal, cfg *config.Config, run *Run, m Manifest, now time.Time) error {
	existing, err := journal.ReadAll[journal.CalibrationResult](j, journal.CalibrationFile)
	if err != nil {
		return err
	}
	type key struct{ task, entry string }
	seen := map[key]bool{}
	for _, c := range existing {
		if c.Kind == "result" && c.Run == run.ID {
			seen[key{c.Task, c.Entry}] = true
		}
	}
	verification := maps.Clone(m.Verification)
	if verification == nil {
		verification = map[string]string{}
	}
	tasks := map[string]ManifestTask{}
	for _, t := range m.Tasks {
		tasks[t.ID] = t
		if t.Verification != "" {
			verification[t.ID] = t.Verification
		}
	}
	for _, c := range run.Cells {
		k := key{c.Task, c.Entry}
		if c.State != CellDone || seen[k] {
			continue
		}
		seen[k] = true
		role := cellRole(cfg, TaskRecord{ManifestTask: tasks[c.Task]})
		r := journal.CalibrationResult{Kind: "result", Time: now.UTC(), Run: run.ID, Task: c.Task, Class: c.Class,
			Ladder: config.LadderKey(c.Class, role, cfg.Learning.Classes[c.Class]), Entry: c.Entry,
			Verification: verification[c.Task], Passed: c.Passed, Source: "calibration"}
		if err := j.Append(journal.CalibrationFile, r); err != nil {
			return err
		}
	}
	return nil
}

// Drop appends a record that drops task from run, so none of its results or
// verdicts count. It refuses a task with no cell in the run.
func Drop(j *journal.Journal, run *Run, task string, now time.Time) error {
	if !slices.ContainsFunc(run.Cells, func(c Cell) bool { return c.Task == task }) {
		return fmt.Errorf("task %s is not in the run", task)
	}
	return j.Append(journal.CalibrationFile, journal.CalibrationResult{Kind: "drop", Time: now.UTC(), Run: run.ID, Task: task, Source: "calibration"})
}

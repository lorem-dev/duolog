package calibration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/budget"
)

// PlanFile is the costed plan of a run, kept in its folder until approved.
const PlanFile = "plan.json"

// Approve records the approval of p. A run without cells gets the plan's
// cells. A run planned again keeps each cell that ran, with its id, files
// and figures, gives the plan's other cells pending ones, reusing the id of
// a cell that did not run, and skips the pending cells the plan left out.
// The plan approved already is refused, so approving twice cannot reset a
// cell.
func Approve(run *Run, p Plan, now time.Time) error {
	if run.Approved != nil && run.Approved.Hash == p.Hash {
		return fmt.Errorf("plan %s is already approved; see duolog calibrate status", p.Hash)
	}
	planned := Cells(p, run.Seed)
	if len(run.Cells) > 0 {
		prior := map[string]Cell{}
		for _, c := range run.Cells {
			k := budget.CellKey(c.Class, c.Task, c.Entry)
			if old, ok := prior[k]; !ok || c.Ran() && !old.Ran() {
				prior[k] = c
			}
		}
		next := nextCellNumber(run.Cells)
		used := map[string]bool{}
		var cells []Cell
		for _, c := range planned {
			if old, ok := prior[budget.CellKey(c.Class, c.Task, c.Entry)]; ok && !used[old.ID] {
				used[old.ID] = true
				if old.Ran() {
					cells = append(cells, old)
					continue
				}
				c.ID = old.ID
			} else {
				c.ID = fmt.Sprintf("c%03d", next)
				next++
			}
			cells = append(cells, c)
		}
		for _, c := range run.Cells {
			if used[c.ID] {
				continue
			}
			if c.State == CellPending || c.State == CellRunning {
				c.State, c.Reason = CellSkipped, "not in the approved plan"
			}
			cells = append(cells, c)
		}
		planned = cells
	}
	run.Cells = planned
	run.Approved = &Approval{Hash: p.Hash, Time: now.UTC()}
	if run.Stopped == stopReapprove {
		run.Stopped = ""
	}
	return nil
}

// nextCellNumber is the number after the highest cNNN cell id.
func nextCellNumber(cells []Cell) int {
	next := len(cells) + 1
	for _, c := range cells {
		if n, err := strconv.Atoi(strings.TrimPrefix(c.ID, "c")); err == nil && n >= next {
			next = n + 1
		}
	}
	return next
}

// NextPlanFile holds a plan shown after an approval: plan.json keeps the
// approved one, which the run follows until the person approves this one.
const NextPlanFile = "plan-next.json"

// SavePlan replaces plan.json in runDir through a temporary file.
func SavePlan(runDir string, p Plan) error {
	return writeJSON(filepath.Join(runDir, PlanFile), p)
}

// SaveNextPlan replaces plan-next.json in runDir.
func SaveNextPlan(runDir string, p Plan) error {
	return writeJSON(filepath.Join(runDir, NextPlanFile), p)
}

func LoadPlan(runDir string) (Plan, error) { return loadPlan(runDir, PlanFile) }

// LoadPlanToApprove reads the plan shown last: plan-next.json, or plan.json
// without one.
func LoadPlanToApprove(runDir string) (Plan, error) {
	p, err := loadPlan(runDir, NextPlanFile)
	if os.IsNotExist(err) {
		return LoadPlan(runDir)
	}
	return p, err
}

func loadPlan(runDir, name string) (Plan, error) {
	var p Plan
	data, err := os.ReadFile(filepath.Join(runDir, name))
	if err != nil {
		return Plan{}, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Plan{}, fmt.Errorf("calibration %s: %w", name, err)
	}
	return p, nil
}

// TaskAges reads the days since each task's commit from its task.json and
// Git; a task without either has no age.
func TaskAges(ctx context.Context, root, runDir string, m Manifest, now time.Time) map[string]int {
	ages := map[string]int{}
	for _, task := range m.Tasks {
		r, err := ReadTask(runDir, task.ID)
		if err != nil || r.Ref == "" {
			continue
		}
		out, err := run(ctx, root, "show", "-s", "--format=%ct", r.Ref, "--")
		if err != nil {
			continue
		}
		unix, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			continue
		}
		ages[task.ID] = int(now.Sub(time.Unix(unix, 0)).Hours() / 24)
	}
	return ages
}

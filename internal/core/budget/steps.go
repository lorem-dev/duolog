package budget

import (
	"fmt"
	"slices"
)

// NextStep names the duolog calibrate command after each step.
var NextStep = map[string]string{
	"survey":  "classes",
	"classes": "mine",
	"mine":    "plan",
	"plan":    "run",
	"run":     "judge",
	"judge":   "propose",
}

// PlanStep refuses to plan a run that has not been mined, or that is past
// the steps that plan: runID is the run's id and step its step, "" without a
// run, and next names the command after each step.
func PlanStep(runID, step string, next map[string]string) error {
	if step == "" || step == "survey" || step == "classes" {
		return fmt.Errorf("run duolog calibrate mine first")
	}
	// A run or judge step plans again too: judges or rubrics changed after
	// the cells ran need a new approval before judging goes on.
	if !slices.Contains([]string{"mine", "plan", "run", "judge"}, step) {
		following := next[step]
		if following == "" {
			following = "survey --new"
		}
		return fmt.Errorf("calibration run %s is at step %s; next: duolog calibrate %s", runID, step, following)
	}
	return nil
}

// CheckApproval refuses to approve p with the hash approve: the plan
// approved already (approvedHash), a plan without a hash, a hash that is not
// p's, and a p edited after it was shown, whose hash no longer matches.
func CheckApproval(approve, approvedHash string, p Plan) error {
	if approvedHash == approve {
		return fmt.Errorf("plan %s is already approved; see duolog calibrate status", approve)
	}
	if p.Hash == "" || approve != p.Hash || p.ComputeHash() != p.Hash {
		return fmt.Errorf("plan hash does not match; run duolog calibrate plan again")
	}
	return nil
}

// Approval is what approving a plan leaves the run at: its Step, the command
// after it in Next, and how many cells that ran it Kept.
type Approval struct {
	Step, Next string
	Kept       int
}

// Approve says where a run at step goes once its plan is approved, given the
// cells it has after the approval. Only a run past its run part can skip
// calibrate run; a first approval goes through it even with no cell to run.
// Planned again after the run part with no cell left to run, the run goes
// back to judging, which keeps the verdicts given.
func Approve(step string, cells []Cell) Approval {
	ranPart := step == "run" || step == "judge"
	a := Approval{Step: "plan", Next: "run"}
	for _, c := range cells {
		if c.Ran() {
			a.Kept++
		}
	}
	if a.Kept > 0 {
		a.Next = "resume"
	}
	if ranPart && !slices.ContainsFunc(cells, func(c Cell) bool {
		return c.State == CellPending || c.State == CellRunning
	}) {
		a.Step, a.Next = "run", "judge"
	}
	return a
}

// PlanSave says what the plan command does with the plan it made.
type PlanSave int

const (
	// SavePlan writes plan.json: nothing is approved yet.
	SavePlan PlanSave = iota
	// SaveNextPlan writes plan-next.json: the approved plan stays in force.
	SaveNextPlan
	// RemoveNextPlan removes plan-next.json: the plan is the approved one.
	RemoveNextPlan
)

// ChoosePlanSave picks the save for a plan with hash, given whether a plan is
// approved and its hash. The approved plan stays in force until the person
// approves a new one, which keeps the cells that ran.
func ChoosePlanSave(approvedHash string, approved bool, hash string) PlanSave {
	switch {
	case !approved:
		return SavePlan
	case approvedHash != hash:
		return SaveNextPlan
	}
	return RemoveNextPlan
}

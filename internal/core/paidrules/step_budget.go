package paidrules

import (
	"errors"
	"fmt"
	"math"
)

// ReasonStepBudget refuses a step whose task does not fit what the approved
// plan still allows it. Waiting does not help.
const ReasonStepBudget = "the approved plan's budget for this step is spent"

// StepBudget is what an approved calibration plan still allows one step,
// declared by the caller and enforced in the same admission as the account
// cap and slots: USD for a paid task, and real fractions of the whole
// weekly and five-hour windows for any other task, as the plan's caps leave
// them after the run's spend. PlanHash and StepID name the step; their key
// tags the step's reservation and its queue ticket, so a resume can tell a
// step whose worker may still run. WaitForSession lets a calibration step
// wait for a session slot without a deadline; without it the step does not
// wait.
type StepBudget struct {
	PlanHash, StepID                 string
	USDRemaining                     float64
	WeeklyRemaining, WindowRemaining float64
	WaitForSession                   bool
}

// Key is the receipt of the step: the tag of its reservation.
func (b *StepBudget) Key() string { return b.PlanHash + "/" + b.StepID }

// validate refuses a budget that names no step or holds an amount that is
// not a finite number in range: USD 0 or more, fractions from 0 to 1.
func (b *StepBudget) validate() error {
	fraction := func(v float64) bool { return v >= 0 && v <= 1 }
	switch {
	case b.PlanHash == "" || b.StepID == "":
		return errors.New("paid runs: a step budget names its plan and step")
	case !(b.USDRemaining >= 0 && b.USDRemaining <= math.MaxFloat64):
		return errors.New("paid runs: a step budget's USD must be a finite number, 0 or more")
	case !fraction(b.WeeklyRemaining) || !fraction(b.WindowRemaining):
		return errors.New("paid runs: a step budget's window shares must be fractions from 0 to 1")
	}
	return nil
}

// check reports why a task of estimate est does not fit the budget, "" when
// it does: a paid task's USD estimate against USDRemaining; any other task's
// cost in its weekly windows (and those of unknown length) and in its
// five-hour windows against the remaining fractions. Window costs are raw
// fractions of the whole window, as the budget's are.
func (b *StepBudget) check(est Estimate, paid bool) string {
	if paid {
		if est.USD > b.USDRemaining+usdEpsilon {
			return fmt.Sprintf("the task is estimated at %.2f USD and the plan leaves %.2f", est.USD, b.USDRemaining)
		}
		return ""
	}
	var weekly, window float64
	for _, w := range est.Windows {
		if w.Reserve == reserveFiveHour {
			window = max(window, w.Cost)
		} else {
			weekly = max(weekly, w.Cost)
		}
	}
	switch {
	case weekly > b.WeeklyRemaining+fitSlack:
		return fmt.Sprintf("the task takes %.3f of a weekly window and the plan leaves %.3f", weekly, b.WeeklyRemaining)
	case window > b.WindowRemaining+fitSlack:
		return fmt.Sprintf("the task takes %.3f of a five-hour window and the plan leaves %.3f", window, b.WindowRemaining)
	}
	return ""
}

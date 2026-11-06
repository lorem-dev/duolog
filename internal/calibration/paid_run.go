package calibration

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/paidruns"
)

// StepAccounts is the user's account state as calibration steps share it
// with every other run: Reconcile ends the reservations whose owners are
// proven gone, Held reports whether a step receipt still holds one, and
// Observe feeds the shared rate learner with one finished step's tokens,
// once, in real units. The command's adapter reads them through the account
// gate; nil leaves a run to its own rates.json, as before.
type StepAccounts interface {
	Reconcile(ctx context.Context) error
	Held(ctx context.Context, receipt string) (bool, error)
	Observe(ctx context.Context, agent, family string, input, cached, output int64) error
}

const (
	// stopHeld ends a resume while an earlier attempt of a step still holds
	// its account reservation: its worker may still run.
	stopHeld = "step still held"
	// capRetry is how long a paid route at its 24-hour cap pauses the run;
	// the resume reads the budget afresh.
	capRetry = time.Hour
)

// stepHolder carries, through a step's context, the budget and the wait
// notice its start set for the step's delegation.
type stepHolder struct {
	budget  *paidruns.StepBudget
	waiting func(bool)
}

type stepKey struct{}

// withStep gives ctx a holder that a step's start fills in.
func withStep(ctx context.Context) (context.Context, *stepHolder) {
	h := &stepHolder{}
	return context.WithValue(ctx, stepKey{}, h), h
}

// StepOptions returns what the runner set for the step that ctx belongs to:
// its budget and its wait notice, for the step's delegate.Options. Outside
// a runner, or before the step's start, both are nil.
func StepOptions(ctx context.Context) (*paidruns.StepBudget, func(bool)) {
	h, _ := ctx.Value(stepKey{}).(*stepHolder)
	if h == nil {
		return nil, nil
	}
	return h.budget, h.waiting
}

// billing is each agent's budget for the run: the stricter one the command
// set, or the approved plan's.
func (r *Runner) billing() map[string]BillingBudget {
	if r.Billing != nil {
		return r.Billing
	}
	return r.Plan.Billing
}

// receipt is the account tag of a step attempt: the approved plan and the
// attempt.
func (r *Runner) receipt(stepID string) string {
	hash := ""
	if r.Run.Approved != nil {
		hash = r.Run.Approved.Hash
	}
	return hash + "/" + stepID
}

// newReceipt gives step its next attempt, as step.n, and records it in the
// run, which the step's start saves.
func (r *Runner) newReceipt(step string) string {
	n := 1
	if last, ok := r.Run.Receipts[step]; ok {
		if i := strings.LastIndexByte(last, '.'); i >= 0 {
			if k, err := strconv.Atoi(last[i+1:]); err == nil {
				n = k + 1
			}
		}
	}
	id := fmt.Sprintf("%s.%d", step, n)
	if r.Run.Receipts == nil {
		r.Run.Receipts = map[string]string{}
	}
	r.Run.Receipts[step] = id
	return id
}

// stepBudget is what the approved plan still allows a step of agent, before
// the step's own charge: the approved USD less the run's paid spend, and for
// a cell less the USD kept for judge calls and rewrites; the weekly and
// five-hour caps of the account's usable share less the run's spend, and
// for a cell less the weekly share kept for judging; as real fractions of
// the whole windows. A step of the run may wait for a session without a
// deadline. Without an approved plan there is no budget to state, and the
// step is admitted as any calibration step is.
func (r *Runner) stepBudget(agent, stepID string, judge bool) *paidruns.StepBudget {
	if r.Run.Approved == nil || r.Run.Approved.Hash == "" {
		return nil
	}
	b := r.billing()[agent]
	u := b.usable()
	cal := r.Cfg.Calibration
	weekly := cal.MaxWeeklyShare*100*u - r.Run.Spend.WeeklyPct[agent]
	usd := b.ApprovedUSD - r.Run.Spend.PaidUSD[agent]
	if !judge {
		weekly -= r.held(agent)
		usd -= r.judgeUSDLeft(agent)
	}
	window := cal.MaxWindowShare*100*u - r.Run.Spend.Own(agent, r.Now())
	return &paidruns.StepBudget{PlanHash: r.Run.Approved.Hash, StepID: stepID, USDRemaining: max(0, usd),
		WeeklyRemaining: fraction(weekly), WindowRemaining: fraction(window), WaitForSession: true}
}

// fraction turns percentage points into a fraction from 0 to 1.
func fraction(pct float64) float64 { return min(1, max(0, pct/100)) }

// judgeUSDLeft is the USD the plan keeps for agent's judge calls and
// rewrites that they have not spent yet.
func (r *Runner) judgeUSDLeft(agent string) float64 {
	var planned float64
	for _, c := range r.Plan.Classes {
		planned += c.JudgeUSD[agent]
	}
	return max(0, planned-r.Run.Spend.JudgeUSD[agent])
}

// chargeUSD counts a step's cost toward the plan's approved USD when the
// agent runs on a paid route; judge marks a judge call or planned rewrite.
func (r *Runner) chargeUSD(agent string, usd float64, judge bool) {
	if !r.billing()[agent].paid() || usd <= 0 {
		return
	}
	if r.Run.Spend.PaidUSD == nil {
		r.Run.Spend.PaidUSD = map[string]float64{}
	}
	r.Run.Spend.PaidUSD[agent] += usd
	if judge {
		if r.Run.Spend.JudgeUSD == nil {
			r.Run.Spend.JudgeUSD = map[string]float64{}
		}
		r.Run.Spend.JudgeUSD[agent] += usd
	}
}

// waiting is the wait notice of a step of agent: the run says it waits for
// a session while the account gate has it wait for a slot, saved so status
// shows it. A failed save leaves only the notice out of date.
func (r *Runner) waiting(agent string) func(bool) {
	return func(on bool) {
		r.Run.WaitReason = ""
		if on {
			r.Run.WaitReason = "waiting for a session of " + agent
		}
		_ = r.Run.Save(r.StateDir)
	}
}

// observe feeds the shared rate learner one finished step, once.
func (r *Runner) observe(ctx context.Context, agent, family string, input, cached, output int64) error {
	if r.Accounts == nil {
		return nil
	}
	return r.Accounts.Observe(ctx, agent, family, input, cached, output)
}

// stillHeld reports whether cell i's last attempt still holds its account
// reservation, after the account state was reconciled: a worker a killed
// supervisor left may still run, and the cell is not run again until it
// ends.
func (r *Runner) stillHeld(ctx context.Context, i int) (bool, error) {
	c := r.Run.Cells[i]
	last := r.Run.Receipts[c.ID]
	if r.Accounts == nil || c.State != CellRunning || last == "" {
		return false, nil
	}
	return r.Accounts.Held(ctx, r.receipt(last))
}

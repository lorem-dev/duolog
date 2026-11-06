package calibration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/delegate"
)

// ErrNotApproved refuses a run whose plan the person has not approved.
var ErrNotApproved = errors.New("approve the plan first: duolog calibrate plan")

// Stop reasons the run adds to the ones of Admit, and the ones it handles.
const (
	stopReapprove     = "re-approve"
	stopProviderLimit = "provider limit"
	stopQuota         = "quota unavailable"
	stopCap           = "approved cap"
)

// pilotCells is how many done cells the pilot and the re-approval check wait
// for; reapproveFactor is how far past the plan's high estimate a projection
// may go before the person approves again.
const (
	pilotCells      = 2
	reapproveFactor = 1.5
)

// minSha is the shortest sha prefix a statement may not hold.
const minSha = 7

// StartFunc admits and charges a step once everything before its worker is
// ready: the tree or judge folder, its isolation and its trust. It reports
// false when admission refused the step; nil admits every step.
type StartFunc func() (bool, error)

// ErrNotAdmitted ends a step whose StartFunc refused it; the stop or pause
// is saved in the run.
var ErrNotAdmitted = errors.New("calibration: step not admitted")

// StartStep calls start, if any, right before a step's worker, and returns
// ErrNotAdmitted when it refused the step.
func StartStep(start StartFunc) error {
	if start == nil {
		return nil
	}
	ok, err := start()
	if err == nil && !ok {
		err = ErrNotAdmitted
	}
	return err
}

// CellFunc runs one cell and calls start right before its worker; RunCell
// with a CellEnv is the real one.
type CellFunc func(ctx context.Context, c Cell, t TaskRecord, start StartFunc) (CellResult, error)

// RewriteFunc writes a new statement for a task from its reference change
// and subject, and calls start right before its worker.
type RewriteFunc func(ctx context.Context, t TaskRecord, start StartFunc) (statement string, tokens int64, err error)

// Runner runs the cells of an approved plan.
type Runner struct {
	StateDir, Root string
	Cfg            *config.Config
	Run            *Run
	Plan           Plan
	Manifest       Manifest
	Quota          *QuotaCache
	Now            func() time.Time
	Cell           CellFunc
	Rewrite        RewriteFunc
	Out            io.Writer
	// Tree is the folder cells work in. A running cell records it, so an
	// interrupt or a resume after a crash removes what the cell left; ""
	// records none.
	Tree string
	// Billing is each agent's budget for this run, Plan.LimitBilling of the
	// account gate's report now; nil means the approved plan's.
	Billing map[string]BillingBudget
	// Accounts is the user's shared account state: reconciled before a
	// resume, asked whether a step's earlier attempt is still held, and fed
	// each finished step's tokens; nil keeps a run to its own rates.json.
	Accounts StepAccounts

	rates map[string]Rate
	// judging says Judge admits the calls, so a pause names the judge step.
	judging bool
	// capped says an agent reached the approved cap and its cells were cut.
	capped bool
}

// Outcome is how Execute ended: a stop reason, a pause until a window
// resets, a stop for a new approval, or every cell done.
type Outcome struct {
	Stopped   string
	Pause     time.Time
	Reapprove bool
	Done      bool
}

// Execute rechecks the approved plan against the repository and the config,
// rewrites the statements that need it, then runs the pending cells in
// order, each under admission, and saves the run after every change. It runs
// under the caps and cell limits of the approved plan, or the config's where
// they are stricter, and keeps the plan's judge share free of cells. It
// stops at the first refusal, provider limit or projection past the plan. An
// agent at the approved cap has its cells cut and the others go on; the run
// part then ends at the cap.
func (r *Runner) Execute(ctx context.Context) (Outcome, error) {
	if r.Run == nil || r.Run.Approved == nil {
		return Outcome{}, ErrNotApproved
	}
	if r.Cfg == nil || r.Quota == nil || r.Cell == nil || r.Rewrite == nil {
		return Outcome{}, errors.New("calibration run: no configuration, quota, cell or rewrite")
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Out == nil {
		r.Out = io.Discard
	}
	if err := Recheck(ctx, r.Root, r.Cfg, r.Run, r.Manifest, r.Plan); err != nil {
		return Outcome{}, err
	}
	// Every step's budget and receipt name the approved plan: no other plan
	// starts a step.
	if r.Plan.Hash != "" && r.Plan.Hash != r.Run.Approved.Hash {
		return Outcome{}, fmt.Errorf("%w: the plan to run is not the approved one", ErrNotApproved)
	}
	r.Cfg = r.Plan.Limit(r.Cfg)
	rates, err := ReadRates(r.StateDir)
	if err != nil {
		return Outcome{}, err
	}
	r.rates, r.capped = rates, false
	r.Run.Stopped, r.Run.Pause, r.Run.WaitReason = "", time.Time{}, ""
	// Before a resume runs anything again, the account holds whose owners are
	// proven gone end; a step whose worker may still run stays held.
	if r.Accounts != nil {
		if err := r.Accounts.Reconcile(ctx); err != nil {
			return Outcome{}, err
		}
	}

	if o, stop, err := r.rewriteAll(ctx); err != nil || stop {
		return o, err
	}
	if err := r.race(); err != nil {
		return Outcome{}, err
	}
	for i := 0; i < len(r.Run.Cells); i++ {
		c := r.Run.Cells[i]
		if c.State != CellPending && c.State != CellRunning {
			continue
		}
		if held, err := r.stillHeld(ctx, i); err != nil {
			return Outcome{}, err
		} else if held {
			if _, err := fmt.Fprintf(r.Out, "cell %s: an earlier attempt still holds its account reservation; its worker may still run, so resume once it ends\n", c.ID); err != nil {
				return Outcome{}, err
			}
			o, err := r.stop(stopHeld)
			return o, err
		}
		o, stop, err := r.runCell(ctx, i)
		if err != nil || stop {
			return o, err
		}
	}
	r.Run.Step = "run"
	if r.capped {
		o, err := r.stop(stopCap)
		if err != nil {
			return o, err
		}
		_, err = fmt.Fprintln(r.Out, "next: duolog calibrate judge")
		return o, err
	}
	if err := r.Run.Save(r.StateDir); err != nil {
		return Outcome{}, err
	}
	_, err = fmt.Fprintln(r.Out, "all cells done\nnext: duolog calibrate judge")
	return Outcome{Done: true}, err
}

// runCell admits, runs and records cell i, then runs the pilot, the
// re-approval check and racing that the result makes due. The cell readies,
// isolates and checks its tree before it calls start, so a cell refused
// before its worker starts is not charged.
func (r *Runner) runCell(ctx context.Context, i int) (Outcome, bool, error) {
	c := r.Run.Cells[i]
	task, err := ReadTask(r.Run.Dir(r.StateDir), c.Task)
	if err != nil {
		return Outcome{}, true, err
	}
	agent, family := entryAgent(c.Entry), r.family(c.Entry)
	cost := CostOf(r.rate(agent), r.Manifest.Estimates[c.Task].TokensHigh, r.measured(agent))
	var before []Window
	var refused *Outcome
	var heldUSD float64
	stepCtx, step := withStep(ctx)
	start := func() (bool, error) {
		ws, o, stop, err := r.admit(ctx, agent, family, cost, r.held(agent))
		if err != nil || stop {
			refused = &o
			return false, err
		}
		// Each start is a new attempt with its own receipt and what the plan
		// still allows it, before its own charge; the account gate admits the
		// cell's worker within that.
		step.budget = r.stepBudget(agent, r.newReceipt(c.ID), false)
		step.waiting = r.waiting(agent)
		// The attempt is charged before the worker starts, its window share
		// and on a paid route its high USD, in the same save that marks the
		// cell running, so an interrupt or a crash keeps it counted once; a
		// rerun is a new attempt.
		before = ws
		r.Run.Spend.Raise(agent, before, cost)
		heldUSD = r.holdUSD(agent, cellUSD(r.Cfg, agent, entryModel(c.Entry), r.Manifest.Estimates[c.Task].TokensHigh), false)
		r.Run.Cells[i].State, r.Run.Cells[i].Tree = CellRunning, r.Tree
		return true, r.Run.Save(r.StateDir)
	}
	res, err := r.Cell(stepCtx, c, task, start)
	switch {
	case refused != nil && (err == nil || errors.Is(err, ErrNotAdmitted)):
		if refused.Stopped == stopCap {
			return Outcome{}, false, r.capAgent(agent)
		}
		return *refused, true, nil
	case err != nil && before == nil:
		// Refused before the worker could start: nothing was charged.
		return Outcome{}, true, err
	case notStarted(err):
		// The delegation refused it after admission, before its worker: the
		// charge is taken back and the cell waits again.
		r.Run.Cells[i].State = CellPending
		r.chargeUSD(agent, -heldUSD, false)
		return Outcome{}, true, errors.Join(err, r.removeTree(i), r.release(agent, before, cost, false))
	case err != nil:
		// The cell stays running for a resume to run again, without its tree.
		// Its USD stays held, raised to what it measured if that is more.
		r.settleUSD(agent, heldUSD, res.USD, false, false)
		return Outcome{}, true, errors.Join(err, r.removeTree(i), r.Run.Save(r.StateDir))
	}
	c = r.Run.Cells[i]
	c.State, c.Passed, c.Reason, c.Tree, c.SetupChanged = res.State, res.Passed, res.Reason, "", res.SetupChanged
	c.Tokens, c.USD, c.Seconds = res.Tokens, res.USD, res.Seconds
	var quotaErr, observeErr error
	if before != nil {
		c.Weekly, c.Window, quotaErr = r.settle(ctx, agent, family, before, cost, false)
		r.settleUSD(agent, heldUSD, res.USD, false, true)
		if quotaErr == nil {
			observeErr = r.observe(ctx, agent, family, res.Input, res.Cached, res.Output)
		}
	}
	r.Run.Cells[i] = c
	if err := r.Run.Save(r.StateDir); err != nil {
		return Outcome{}, true, errors.Join(err, observeErr)
	}
	if observeErr != nil {
		return Outcome{}, true, fmt.Errorf("calibration cell %s: shared rates: %w", c.ID, observeErr)
	}
	if _, err := fmt.Fprintf(r.Out, "cell %s %s %s %s: %s, %dk tokens, %dm\n", r.position(i), c.Class, c.Entry, c.Task,
		cellText(c), int64(math.Round(float64(c.Tokens)/1000)), int64(math.Round(c.Seconds/60))); err != nil {
		return Outcome{}, true, err
	}
	// A turn cap is a limit of this cell alone; the run goes on.
	if c.State == CellFailedLimit && c.Reason == stopProviderLimit {
		o, err := r.stop(stopProviderLimit)
		return o, true, err
	}
	if quotaErr != nil {
		o, err := r.quotaStop(agent, quotaErr)
		return o, true, err
	}
	if c.State == CellDone && r.count(CellDone) == pilotCells {
		if o, stop, err := r.afterPilot(ctx); err != nil || stop {
			return o, true, err
		}
	}
	return Outcome{}, false, r.race()
}

// removeTree empties the tree cell i recorded and clears it from the cell.
// The Codex hooks the person approved there stay; a tree left empty goes. A
// tree that holds the project or lies inside it is refused, not removed.
func (r *Runner) removeTree(i int) error {
	tree := r.Run.Cells[i].Tree
	if tree == "" {
		return nil
	}
	if inside(tree, r.Root) || inside(r.Root, tree) {
		return fmt.Errorf("calibration cell %s: tree %s is not outside the project %s", r.Run.Cells[i].ID, tree, r.Root)
	}
	if err := clearTree(tree); err != nil {
		return err
	}
	if entries, err := os.ReadDir(tree); err == nil && len(entries) == 0 {
		if err := os.Remove(tree); err != nil {
			return err
		}
	}
	r.Run.Cells[i].Tree = ""
	return nil
}

// rewriteAll rewrites every task of Manifest.Rewrite that still has cells to
// run, before any cell. A refused statement makes the task's cells invalid
// and gives them to the next reserve task of its class, which is rewritten
// in turn when it needs it.
func (r *Runner) rewriteAll(ctx context.Context) (Outcome, bool, error) {
	if len(r.Manifest.Rewrite) == 0 {
		return Outcome{}, false, nil
	}
	if len(r.Cfg.Learning.Judges) == 0 {
		return Outcome{}, true, errors.New("calibration run: no judge to rewrite statements")
	}
	judge := r.Cfg.Learning.Judges[0]
	agent, family := judge.Agent, modelFamily(r.Cfg, judge.Model)
	runDir := r.Run.Dir(r.StateDir)
	// A rewrite may copy a name from the reference change, so it passes
	// the same privacy policy as the statements mining found.
	names, err := HistoryNames(ctx, r.Root)
	if err != nil {
		return Outcome{}, true, fmt.Errorf("calibration run: the history's names: %w", err)
	}
	queue := slices.Clone(r.Manifest.Rewrite)
	var quotaErr error
	for len(queue) > 0 && quotaErr == nil {
		id := queue[0]
		queue = queue[1:]
		if !r.hasWork(id) {
			continue
		}
		task, err := ReadTask(runDir, id)
		if err != nil {
			return Outcome{}, true, err
		}
		if !task.NeedsRewrite {
			continue
		}
		// Costed as the plan costs it: a judge call of the task's class. Only
		// a planned task's rewrite spends the judge share, at that estimate;
		// a reserve task's and any rise above the estimate count toward the
		// cap alone, so the judging part of the share stays held.
		cost := JudgeCost(r.rate(agent), classTokens(r.Manifest, task.Class))
		planned := r.planned(id)
		var before []Window
		var refused *Outcome
		var heldUSD float64
		stepCtx, step := withStep(ctx)
		// The rewrite readies and checks the judge folder before it calls
		// start; then it is charged before it starts, like a cell, so a
		// failed rewrite counts and a refused one does not.
		start := func() (bool, error) {
			ws, o, stop, err := r.admit(ctx, agent, family, cost, 0)
			if err != nil || stop {
				refused = &o
				return false, err
			}
			step.budget = r.stepBudget(agent, r.newReceipt("rewrite-"+id), true)
			step.waiting = r.waiting(agent)
			before = ws
			r.Run.Spend.Raise(agent, before, cost)
			if planned {
				r.Run.Spend.addJudge(agent, cost.WeeklyPct)
			}
			heldUSD = r.holdUSD(agent, cellUSD(r.Cfg, agent, judge.Model, judgeTokensHigh), planned)
			return true, r.Run.Save(r.StateDir)
		}
		statement, tokens, err := r.Rewrite(stepCtx, task, start)
		if refused != nil && (err == nil || errors.Is(err, ErrNotAdmitted)) {
			if refused.Stopped != stopCap {
				return *refused, true, nil
			}
			// Without a statement the tasks still to rewrite cannot run.
			r.cutTask(id)
			for _, id := range queue {
				if rec, err := ReadTask(runDir, id); err != nil {
					return Outcome{}, true, err
				} else if rec.NeedsRewrite {
					r.cutTask(id)
				}
			}
			return Outcome{}, false, r.capAgent(agent)
		}
		if err != nil {
			err = fmt.Errorf("calibration task %s: rewrite: %w", id, err)
			switch {
			case before != nil && notStarted(err):
				r.chargeUSD(agent, -heldUSD, planned)
				err = errors.Join(err, r.release(agent, before, cost, planned))
			case before != nil:
				// A rewrite that broke off keeps its held USD.
				r.settleUSD(agent, heldUSD, cellUSD(r.Cfg, agent, judge.Model, tokens), planned, false)
				err = errors.Join(err, r.Run.Save(r.StateDir))
			}
			return Outcome{}, true, err
		}
		if before != nil {
			// A failed reading stops the run once this statement is kept.
			_, _, quotaErr = r.settle(ctx, agent, family, before, cost, false)
			r.settleUSD(agent, heldUSD, cellUSD(r.Cfg, agent, judge.Model, tokens), planned, true)
			if quotaErr == nil {
				// A rewrite reports its tokens only: they count as input.
				if err := r.observe(ctx, agent, family, tokens, 0, 0); err != nil {
					return Outcome{}, true, fmt.Errorf("calibration task %s: rewrite: shared rates: %w", id, err)
				}
			}
		}
		statement = strings.TrimSpace(statement)
		if verr := ValidStatement(statement, task, names); verr != nil {
			reserve, err := r.replace(id, "statement refused: "+verr.Error())
			if err != nil {
				return Outcome{}, true, err
			}
			line := fmt.Sprintf("task %s: rewritten statement refused: %v; ", id, verr)
			if reserve == "" {
				line += "no reserve task left, its cells are invalid"
			} else {
				line += "reserve task " + reserve + " takes its cells"
				queue = append(queue, reserve)
			}
			if _, err := fmt.Fprintln(r.Out, line); err != nil {
				return Outcome{}, true, err
			}
			continue
		}
		if err := r.saveStatement(task, statement); err != nil {
			return Outcome{}, true, err
		}
		if _, err := fmt.Fprintf(r.Out, "task %s: statement rewritten, %dk tokens\n", id, int64(math.Round(float64(tokens)/1000))); err != nil {
			return Outcome{}, true, err
		}
	}
	if quotaErr != nil {
		o, err := r.quotaStop(agent, quotaErr)
		return o, true, err
	}
	return Outcome{}, false, nil
}

// saveStatement replaces the task's statement in statement.txt, task.json
// and the manifest.
func (r *Runner) saveStatement(task TaskRecord, statement string) error {
	runDir := r.Run.Dir(r.StateDir)
	dir := filepath.Join(runDir, TasksDir, task.ID)
	if err := os.WriteFile(filepath.Join(dir, "statement.txt"), []byte(statement+"\n"), 0600); err != nil {
		return err
	}
	task.Statement, task.NeedsRewrite = statement, false
	if err := writeJSON(filepath.Join(dir, "task.json"), task); err != nil {
		return err
	}
	for i := range r.Manifest.Tasks {
		if r.Manifest.Tasks[i].ID == task.ID {
			r.Manifest.Tasks[i].Statement = statement
		}
	}
	return SaveManifest(runDir, r.Manifest)
}

// replace makes the cells of task invalid and gives each a pending copy on
// the next reserve task of the task's class, right after it. It returns the
// reserve task, or "" when the class has none left.
func (r *Runner) replace(task, reason string) (string, error) {
	class := ""
	for _, c := range r.Run.Cells {
		if c.Task == task {
			class = c.Class
			break
		}
	}
	reserve := ""
	if pool := r.Manifest.Reserve[class]; len(pool) > 0 {
		reserve = pool[0]
		r.Manifest.Reserve[class] = pool[1:]
	}
	next := r.nextID()
	var cells []Cell
	for _, c := range r.Run.Cells {
		if c.Task != task || c.State != CellPending && c.State != CellRunning {
			cells = append(cells, c)
			continue
		}
		moved := c
		c.State, c.Reason, c.Tree = CellInvalid, reason, ""
		cells = append(cells, c)
		if reserve != "" {
			moved.ID, moved.Task, moved.State, moved.Reason, moved.Tree = fmt.Sprintf("c%03d", next), reserve, CellPending, "", ""
			next++
			cells = append(cells, moved)
		}
	}
	r.Run.Cells = cells
	i := slices.IndexFunc(r.Manifest.Tasks, func(t ManifestTask) bool { return t.ID == task })
	if i >= 0 {
		r.Manifest.Tasks = slices.Delete(r.Manifest.Tasks, i, i+1)
	}
	r.Manifest.Round2 = slices.DeleteFunc(r.Manifest.Round2, func(id string) bool { return id == task })
	r.Manifest.Excluded = append(r.Manifest.Excluded, Exclusion{ID: task, Reason: reason})
	if reserve != "" {
		rec, err := ReadTask(r.Run.Dir(r.StateDir), reserve)
		if err != nil {
			return "", err
		}
		r.Manifest.Tasks = slices.Insert(r.Manifest.Tasks, max(i, 0), rec.ManifestTask)
		if slices.ContainsFunc(r.Run.Cells, func(c Cell) bool { return c.Task == reserve && c.Round == 2 }) {
			r.Manifest.Round2 = append(r.Manifest.Round2, reserve)
		}
	}
	if err := SaveManifest(r.Run.Dir(r.StateDir), r.Manifest); err != nil {
		return "", err
	}
	return reserve, r.Run.Save(r.StateDir)
}

// nextID is the number after the highest cNNN cell id.
func (r *Runner) nextID() int { return nextCellNumber(r.Run.Cells) }

// hasWork reports whether a task has a cell still to run.
func (r *Runner) hasWork(task string) bool {
	return slices.ContainsFunc(r.Run.Cells, func(c Cell) bool {
		return c.Task == task && (c.State == CellPending || c.State == CellRunning)
	})
}

// admit reads the agent's quota from the cache and decides on a step of the
// given cost on a model of family, with held weekly points of the cap kept
// for judging. A pause or a stop is saved and ends the run, but for the
// approved cap outside judging: that one is returned unsaved, as the caller
// cuts the agent's cells and goes on. The reading is returned to measure the
// step against.
func (r *Runner) admit(ctx context.Context, agent, family string, cost CellCost, held float64) ([]Window, Outcome, bool, error) {
	ws, err := r.Quota.Get(ctx, agent, false)
	if err != nil {
		if _, err := fmt.Fprintf(r.Out, "quota of %s unavailable: %v\n", agent, err); err != nil {
			return nil, Outcome{}, true, err
		}
		ws = nil
	}
	ws = r.Run.Spend.WithResets(agent, forModel(ws, family), r.Now())
	r.Run.Spend.Track(agent, ws)
	// Steps run one at a time: one still open now ended without a settle.
	r.Run.Spend.End(agent, ws)
	billing := r.Billing
	if billing == nil {
		billing = r.Plan.Billing
	}
	d := AdmitBilled(agent, billing[agent], ws, cost, r.Run.Spend, r.Cfg.Calibration, held, r.Now())
	switch {
	case d.Admit:
		return ws, Outcome{}, false, nil
	case !d.Wait.IsZero():
		r.Run.Pause = d.Wait
		if err := r.Run.Save(r.StateDir); err != nil {
			return nil, Outcome{}, true, err
		}
		again := "resume with duolog calibrate resume"
		if r.judging {
			again = "judge again with duolog calibrate judge"
		}
		_, err := fmt.Fprintf(r.Out, "paused until %s; %s\n", d.Wait.UTC().Format(time.RFC3339), again)
		return nil, Outcome{Pause: d.Wait}, true, err
	case d.Stop == stopCap && !r.judging:
		return nil, Outcome{Stopped: stopCap}, true, nil
	case d.Stop == stopReapprove:
		o, err := r.reapprove(fmt.Sprintf("the judge share of %s, %s%% weekly, no longer fits under the approved cap after %s%% spent",
			agent, number(math.Round(held*10)/10), number(math.Round(r.Run.Spend.WeeklyPct[agent]*10)/10)))
		return nil, o, true, err
	default:
		o, err := r.stop(d.Stop)
		return nil, o, true, err
	}
}

// held is the part of the agent's approved cap the plan keeps for its judge
// calls and rewrites and that they have not spent yet. It is held against
// judge spend only: judge calls and the estimates of planned rewrites.
func (r *Runner) held(agent string) float64 {
	return max(0, r.Plan.JudgeShare[agent]-r.Run.Spend.JudgePct[agent])
}

// planned reports whether task is one of the approved plan's tasks, whose
// rewrite the judge share holds; a reserve task taking its cells is not.
func (r *Runner) planned(task string) bool {
	return slices.ContainsFunc(r.Plan.Classes, func(c ClassPlan) bool {
		return slices.Contains(c.Tasks, task) || slices.Contains(c.Round2, task)
	})
}

// settle reads the agent's quota afresh after a step that Raise charged at
// its estimate, and ends the step's charges at that reading. A measured
// weekly rise above the estimate replaces it, though the lead may have spent
// some of it: unknown spend counts against calibration. A smaller rise may
// be a late reading, so the estimate stays. judge says the step was a judge
// call, whose rise above the estimate is judge spend too; a rewrite's counts
// toward the cap alone. It returns the measured rises, 0 for one not
// measured, and the read error, which leaves the estimate counted.
func (r *Runner) settle(ctx context.Context, agent, family string, before []Window, cost CellCost, judge bool) (weekly, window float64, err error) {
	after, err := r.Quota.Get(ctx, agent, true)
	if err != nil {
		return 0, 0, err
	}
	after = r.Run.Spend.WithResets(agent, forModel(after, family), r.Now())
	weekly = rise(before, after, KindWeekly)
	window = rise(before, after, KindFiveHour)
	r.Run.Spend.Track(agent, after)
	r.Run.Spend.End(agent, after)
	if weekly > cost.WeeklyPct {
		r.Run.Spend.WeeklyPct[agent] += weekly - cost.WeeklyPct
		if judge {
			r.Run.Spend.addJudge(agent, weekly-cost.WeeklyPct)
		}
	}
	return weekly, window, nil
}

// release takes back the charge of the agent's step that the delegation
// refused before its worker started, and saves the run; judge says the
// charge spent the judge share too.
func (r *Runner) release(agent string, before []Window, cost CellCost, judge bool) error {
	r.Run.Spend.Release(agent, before, cost)
	if judge && r.Run.Spend.JudgePct != nil {
		r.Run.Spend.JudgePct[agent] = max(0, r.Run.Spend.JudgePct[agent]-cost.WeeklyPct)
	}
	return r.Run.Save(r.StateDir)
}

// notStarted reports whether err is a delegation refused before any worker
// started.
func notStarted(err error) bool {
	var ns *delegate.NotStartedError
	return errors.As(err, &ns)
}

// addJudge counts weekly points of the agent's spend as judging's.
func (s *Spend) addJudge(agent string, pct float64) {
	if s.JudgePct == nil {
		s.JudgePct = map[string]float64{}
	}
	s.JudgePct[agent] += pct
}

// learnRate gives an agent without a measured rate the one a judge call of
// tokens measured, when it moved the weekly reading; the rise may hold the
// lead's work, so the rate errs high. A call that moved no weekly quota sets
// none: a zero weekly rate would cost every later step at the whole bound,
// so the agent keeps the scaled cost of an unknown rate.
func (r *Runner) learnRate(agent string, tokens int64, weekly, window float64) error {
	if _, ok := r.rates[agent]; ok || tokens <= 0 || weekly <= 0 {
		return nil
	}
	mtok := float64(tokens) / 1e6
	r.rates[agent] = Rate{WeeklyPerMTok: weekly / mtok, WindowPerMTok: window / mtok}
	if err := WriteRates(r.StateDir, r.rates); err != nil {
		return err
	}
	rate := r.rates[agent]
	_, err := fmt.Fprintf(r.Out, "judge rate %s: %.2f%% weekly, %.2f%% five-hour per 1M tokens\n", agent, rate.WeeklyPerMTok, rate.WindowPerMTok)
	return err
}

// rise is how far the account's window of kind rose between two readings; a
// window that reset in between, or one that did not rise, measures 0.
func rise(before, after []Window, kind string) float64 {
	b, a := accountWindow(before, kind), accountWindow(after, kind)
	if b == nil || a == nil || a.ResetsAt.After(b.ResetsAt.Add(resetSlack)) {
		return 0
	}
	return max(0, a.UsedPercent-b.UsedPercent)
}

// stop saves the run with the reason and ends it.
func (r *Runner) stop(reason string) (Outcome, error) {
	r.Run.Stopped = reason
	if err := r.Run.Save(r.StateDir); err != nil {
		return Outcome{}, err
	}
	_, err := fmt.Fprintf(r.Out, "stopped: %s\n", reason)
	return Outcome{Stopped: reason}, err
}

// quotaStop stops the run after a failed reading; the step before it stays
// counted at its estimate.
func (r *Runner) quotaStop(agent string, err error) (Outcome, error) {
	if _, err := fmt.Fprintf(r.Out, "quota of %s unavailable: %v\n", agent, err); err != nil {
		return Outcome{}, err
	}
	return r.stop(stopQuota)
}

// capAgent cuts the agent's cells left at the approved cap: the approved
// total does not reset, so a resume could never run them. They are skipped
// as cut to cap, the other agents' cells go on, and the run part then ends
// at the cap. Judging stops at the cap without this.
func (r *Runner) capAgent(agent string) error {
	cut := 0
	for i, c := range r.Run.Cells {
		if entryAgent(c.Entry) != agent || c.State != CellPending && c.State != CellRunning {
			continue
		}
		if err := r.removeTree(i); err != nil {
			return err
		}
		r.Run.Cells[i].State, r.Run.Cells[i].Reason = CellSkipped, "cut to cap"
		cut++
	}
	r.capped = true
	if err := r.Run.Save(r.StateDir); err != nil {
		return err
	}
	_, err := fmt.Fprintf(r.Out, "approved cap of %s: %d cells cut to cap\n", agent, cut)
	return err
}

// cutTask skips the cells of task left to run as cut to cap.
func (r *Runner) cutTask(task string) {
	for i, c := range r.Run.Cells {
		if c.Task == task && c.State == CellPending {
			r.Run.Cells[i].State, r.Run.Cells[i].Reason = CellSkipped, "cut to cap"
		}
	}
}

// reapprove stops the run for a new approval, saying why in line.
func (r *Runner) reapprove(line string) (Outcome, error) {
	if _, err := fmt.Fprintln(r.Out, line); err != nil {
		return Outcome{}, err
	}
	o, err := r.stop(stopReapprove)
	if err != nil {
		return o, err
	}
	o.Reapprove = true
	_, err = fmt.Fprintln(r.Out, "plan again: duolog calibrate plan")
	return o, err
}

// afterPilot runs once the run has its first done cells: a pilot measures
// the rates, a projection past the plan stops for a new approval, and new
// rates rebuild the plan and cut it to the cap.
func (r *Runner) afterPilot(ctx context.Context) (Outcome, bool, error) {
	measured := false
	if r.Plan.Pilot {
		var err error
		if measured, err = r.pilot(); err != nil {
			return Outcome{}, true, err
		}
	}
	var tokens int64
	done, cells := 0, 0
	for _, c := range r.Run.Cells {
		switch c.State {
		case CellDone:
			tokens += c.Tokens
			done++
			cells++
		case CellInvalid, CellSkipped:
		default:
			cells++
		}
	}
	var high int64
	for _, c := range r.Plan.Classes {
		high += c.TokensHigh
	}
	if projected := float64(tokens) / float64(done) * float64(cells); high > 0 && projected > reapproveFactor*float64(high) {
		o, err := r.reapprove(fmt.Sprintf("projected %dk tokens, above %s x the plan's %dk",
			int64(math.Round(projected/1000)), number(reapproveFactor), int64(math.Round(float64(high)/1000))))
		return o, true, err
	}
	if measured {
		return Outcome{}, false, r.cut(ctx)
	}
	return Outcome{}, false, nil
}

// pilot measures each agent's rate from its done cells and writes the
// positive ones; it reports whether it wrote any.
func (r *Runner) pilot() (bool, error) {
	type sum struct {
		weekly, window float64
		tokens         int64
	}
	sums := map[string]sum{}
	var agents []string
	for _, c := range r.Run.Cells {
		if c.State != CellDone {
			continue
		}
		agent := entryAgent(c.Entry)
		if _, ok := sums[agent]; !ok {
			agents = append(agents, agent)
		}
		s := sums[agent]
		s.weekly, s.window, s.tokens = s.weekly+c.Weekly, s.window+c.Window, s.tokens+c.Tokens
		sums[agent] = s
	}
	slices.Sort(agents)
	var moved, still []string
	for _, agent := range agents {
		s := sums[agent]
		if s.tokens <= 0 || s.weekly <= 0 && s.window <= 0 {
			still = append(still, agent)
			continue
		}
		mtok := float64(s.tokens) / 1e6
		r.rates[agent] = Rate{WeeklyPerMTok: s.weekly / mtok, WindowPerMTok: s.window / mtok}
		moved = append(moved, agent)
	}
	if len(moved) == 0 {
		_, err := fmt.Fprintln(r.Out, "pilot showed no measurable movement; the conservative bound stays")
		return false, err
	}
	if err := WriteRates(r.StateDir, r.rates); err != nil {
		return false, err
	}
	for _, agent := range moved {
		rate := r.rates[agent]
		if _, err := fmt.Fprintf(r.Out, "pilot rate %s: %.2f%% weekly, %.2f%% five-hour per 1M tokens\n", agent, rate.WeeklyPerMTok, rate.WindowPerMTok); err != nil {
			return false, err
		}
	}
	for _, agent := range still {
		if _, err := fmt.Fprintf(r.Out, "pilot showed no measurable movement for %s; the conservative bound stays\n", agent); err != nil {
			return false, err
		}
	}
	return true, nil
}

// cut rebuilds the plan with the measured rates, the cells that ran and the
// run's spend, and skips the pending cells the rebuilt plan no longer holds.
// It needs no new approval: a cut plan only spends less.
func (r *Runner) cut(ctx context.Context) error {
	quota := map[string][]Window{}
	for _, c := range r.Run.Cells {
		agent := entryAgent(c.Entry)
		if _, ok := quota[agent]; ok {
			continue
		}
		if ws, err := r.Quota.Get(ctx, agent, false); err == nil {
			quota[agent] = ws
		}
	}
	p, err := BuildPlan(PlanInput{Cfg: r.Cfg, Manifest: r.Manifest, Rates: r.rates, Quota: quota, RunID: r.Run.ID,
		Cells: r.Run.Cells, Spent: r.Run.Spend.WeeklyPct})
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, c := range p.Classes {
		for _, task := range slices.Concat(c.Tasks, c.Round2) {
			for _, e := range c.Entries {
				keep[c.Class+"\x00"+task+"\x00"+e.Key] = true
			}
		}
	}
	cut := 0
	for i, c := range r.Run.Cells {
		if c.State == CellPending && !keep[c.Class+"\x00"+c.Task+"\x00"+c.Entry] {
			r.Run.Cells[i].State, r.Run.Cells[i].Reason = CellSkipped, "cut to cap"
			cut++
		}
	}
	r.Plan = p
	if cut == 0 {
		return nil
	}
	if err := r.Run.Save(r.StateDir); err != nil {
		return err
	}
	_, err = fmt.Fprintf(r.Out, "plan estimated again with the pilot rates: %d cells cut to cap\n", cut)
	return err
}

// race applies Race to each class whose round 1 is over and saves the run
// when it skipped a cell.
func (r *Runner) race() error {
	changed := false
	for _, class := range r.Plan.Classes {
		var idx []int
		var cells []Cell
		open := false
		for i, c := range r.Run.Cells {
			if c.Class != class.Class {
				continue
			}
			idx, cells = append(idx, i), append(cells, c)
			if c.Round == 1 && (c.State == CellPending || c.State == CellRunning) {
				open = true
			}
		}
		if open || len(cells) == 0 {
			continue
		}
		order := make([]string, len(class.Entries))
		for i, e := range class.Entries {
			order[i] = e.Key
		}
		raced := map[string]bool{}
		for k, c := range Race(cells, order) {
			if c.State != r.Run.Cells[idx[k]].State {
				r.Run.Cells[idx[k]] = c
				raced[c.Entry] = true
				changed = true
			}
		}
		for _, entry := range slices.Sorted(maps.Keys(raced)) {
			if _, err := fmt.Fprintf(r.Out, "race: %s leaves round 2 of %s\n", entry, class.Class); err != nil {
				return err
			}
		}
	}
	if !changed {
		return nil
	}
	return r.Run.Save(r.StateDir)
}

// Race returns cells with the pending round-2 cells skipped of every entry
// that failed at least 2 of its round-1 tasks of a class while a cheaper or
// equal entry passed every one of those tasks. An entry is cheaper or equal
// when it comes earlier in entryOrder or has a lower mean USD in cells.
func Race(cells []Cell, entryOrder []string) []Cell {
	out := slices.Clone(cells)
	rank := func(entry string) int {
		if i := slices.Index(entryOrder, entry); i >= 0 {
			return i
		}
		return len(entryOrder)
	}
	type key struct{ class, entry string }
	failed := map[key][]string{}
	passed := map[key]map[string]bool{}
	usd := map[key][2]float64{} // sum, count
	var classes []string
	entries := map[string][]string{}
	for _, c := range cells {
		k := key{c.Class, c.Entry}
		if !slices.Contains(classes, c.Class) {
			classes = append(classes, c.Class)
		}
		if !slices.Contains(entries[c.Class], c.Entry) {
			entries[c.Class] = append(entries[c.Class], c.Entry)
		}
		if c.State != CellDone {
			continue
		}
		u := usd[k]
		usd[k] = [2]float64{u[0] + c.USD, u[1] + 1}
		if c.Round != 1 {
			continue
		}
		if c.Passed {
			if passed[k] == nil {
				passed[k] = map[string]bool{}
			}
			passed[k][c.Task] = true
		} else {
			failed[k] = append(failed[k], c.Task)
		}
	}
	// cheaper reports whether a is cheaper than or equal to b.
	cheaper := func(a, b key) bool {
		ua, ub := usd[a], usd[b]
		return rank(a.entry) < rank(b.entry) || ua[1] > 0 && ub[1] > 0 && ua[0]/ua[1] < ub[0]/ub[1]
	}
	losers := map[key]bool{}
	for _, class := range classes {
		for _, e := range entries[class] {
			k := key{class, e}
			if len(failed[k]) < 2 {
				continue
			}
			for _, other := range entries[class] {
				o := key{class, other}
				if other != e && cheaper(o, k) && !slices.ContainsFunc(failed[k], func(task string) bool { return !passed[o][task] }) {
					losers[k] = true
					break
				}
			}
		}
	}
	for i, c := range out {
		if c.Round == 2 && c.State == CellPending && losers[key{c.Class, c.Entry}] {
			out[i].State, out[i].Reason = CellSkipped, "raced out"
		}
	}
	return out
}

// ValidStatement refuses a rewritten statement that holds a sha prefix of
// one of the task's commits, the commit subject, a fenced code block, or
// anything the privacy policy would remove or flag, with names, the
// history's people; or that leaves out a name the hidden tests call.
func ValidStatement(s string, t TaskRecord, names []string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("empty statement")
	}
	lower := strings.ToLower(s)
	for _, c := range t.Commits {
		if len(c) >= minSha && strings.Contains(lower, strings.ToLower(c[:minSha])) {
			return errors.New("statement holds a commit sha")
		}
	}
	if subject := strings.TrimSpace(t.Subject); subject != "" && strings.Contains(lower, strings.ToLower(subject)) {
		return errors.New("statement holds the commit subject")
	}
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			return errors.New("statement holds a fenced code block")
		}
	}
	if err := newPrivacy(names).check(s); err != nil {
		return fmt.Errorf("statement %w", err)
	}
	if name := missingName(s, t.HiddenNames); name != "" {
		return fmt.Errorf("statement does not name %s", name)
	}
	return nil
}

// family is the model family of an entry key, for the model's own limits.
func (r *Runner) family(entry string) string {
	parts := strings.SplitN(entry, "/", 3)
	if len(parts) < 2 {
		return ""
	}
	return modelFamily(r.Cfg, parts[1])
}

func (r *Runner) rate(agent string) *Rate {
	if rate, ok := r.rates[agent]; ok {
		return &rate
	}
	return nil
}

// measured is the measured cost of the agent's done cells.
func (r *Runner) measured(agent string) []CellCost {
	var out []CellCost
	for _, c := range r.Run.Cells {
		if c.State == CellDone && entryAgent(c.Entry) == agent && (c.Weekly > 0 || c.Window > 0) {
			out = append(out, CellCost{WeeklyPct: c.Weekly, WindowPct: c.Window})
		}
	}
	return out
}

func (r *Runner) count(state CellState) int {
	n := 0
	for _, c := range r.Run.Cells {
		if c.State == state {
			n++
		}
	}
	return n
}

// position is "<n>/<total>" of cell i among the cells that are not invalid.
func (r *Runner) position(i int) string {
	n, total := 0, 0
	for j, c := range r.Run.Cells {
		if c.State == CellInvalid {
			continue
		}
		total++
		if j <= i {
			n++
		}
	}
	return fmt.Sprintf("%d/%d", n, total)
}

// cellText is passed or failed for a checked cell, else the reason it ended;
// a check_setup_changed cell names the files first.
func cellText(c Cell) string {
	switch {
	case c.State == CellSetupChanged:
		return "check setup changed: " + strings.Join(c.SetupChanged, ", ") + "; " + cellText(Cell{Passed: c.Passed, Reason: c.Reason})
	case c.Reason != "":
		return c.Reason
	case c.Passed:
		return "passed"
	default:
		return "failed"
	}
}

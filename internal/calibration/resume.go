package calibration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
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

// commitSource prefixes a task source that names one of its commits.
const commitSource = "commit "

// Recheck refuses to resume a run whose approved plan no longer fits the
// repository or the config: a commit of a task with cells left is gone, or a
// class's checks, the judges or their rubrics differ from the ones the plan
// was approved with.
func Recheck(ctx context.Context, root string, cfg *config.Config, run *Run, m Manifest, p Plan) error {
	if cfg == nil || run == nil {
		return errors.New("calibration recheck: no configuration or run")
	}
	for _, t := range m.Tasks {
		if !slices.ContainsFunc(run.Cells, func(c Cell) bool {
			return c.Task == t.ID && (c.State == CellPending || c.State == CellRunning)
		}) {
			continue
		}
		for _, source := range t.Sources {
			sha, ok := strings.CutPrefix(source, commitSource)
			if ok && !commitExists(ctx, root, sha) {
				return fmt.Errorf("commit %s of task %s is gone; plan again", sha, t.ID)
			}
		}
	}
	for _, class := range slices.Sorted(maps.Keys(p.Checks)) {
		if !slices.Equal(p.Checks[class], cfg.Learning.Classes[class].Checks) {
			return fmt.Errorf("checks of class %s changed since approval; plan again", class)
		}
	}
	return p.CheckJudges(cfg)
}

func commitExists(ctx context.Context, root, sha string) bool {
	_, err := run(ctx, root, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// Resume continues a stopped, paused or interrupted run under its approval.
// Before the pause ends it returns the same pause and runs nothing. Otherwise
// it removes the trees of cells left running and makes them pending, reads
// quota afresh and runs as Execute does, which rechecks the repository and
// keeps to the approved limits. Spend is kept, so the approved total does
// not reset with a quota window, and an attempt left running stays charged.
func (r *Runner) Resume(ctx context.Context) (Outcome, error) {
	if r.Run == nil || r.Run.Approved == nil {
		return Outcome{}, ErrNotApproved
	}
	if r.Cfg == nil || r.Quota == nil {
		return Outcome{}, errors.New("calibration resume: no configuration or quota")
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Out == nil {
		r.Out = io.Discard
	}
	if pause := r.Run.Pause; !pause.IsZero() && r.Now().Before(pause) {
		_, err := fmt.Fprintf(r.Out, "paused until %s; resume with duolog calibrate resume\n", pause.UTC().Format(time.RFC3339))
		return Outcome{Pause: pause}, err
	}
	var agents []string
	for i, c := range r.Run.Cells {
		if c.State == CellRunning {
			if err := r.removeTree(i); err != nil {
				return Outcome{}, err
			}
			r.Run.Cells[i].State = CellPending
		}
		if agent := entryAgent(c.Entry); r.Run.Cells[i].State == CellPending && !slices.Contains(agents, agent) {
			agents = append(agents, agent)
		}
	}
	if err := r.Run.Save(r.StateDir); err != nil {
		return Outcome{}, err
	}
	for _, agent := range agents {
		// A failed read is not cached; admission reads again and stops.
		_, _ = r.Quota.Get(ctx, agent, true)
	}
	return r.Execute(ctx)
}

// WriteStatus prints the run: its id and step, the cells per state, the
// spend per agent next to the cap, the pause in UTC, the stop reason and the
// next command.
func WriteStatus(w io.Writer, run *Run, p Plan, now time.Time) error {
	var b strings.Builder
	fmt.Fprintf(&b, "calibration run %s, created %s\nstep: %s\n", run.ID, run.Created.UTC().Format(time.RFC3339), run.Step)
	if len(run.Cells) > 0 {
		counts := map[CellState]int{}
		for _, c := range run.Cells {
			counts[c.State]++
		}
		parts := []string{
			fmt.Sprintf("done %d", counts[CellDone]),
			fmt.Sprintf("pending %d", counts[CellPending]),
			fmt.Sprintf("running %d", counts[CellRunning]),
		}
		for _, state := range []CellState{CellFailedLimit, CellInvalid, CellSkipped, CellLeakage, CellSetupChanged} {
			if counts[state] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", state, counts[state]))
			}
		}
		fmt.Fprintf(&b, "cells: %s\n", strings.Join(parts, ", "))
	}
	if run.Approved != nil {
		fmt.Fprintf(&b, "approved: plan %s at %s\n", run.Approved.Hash, run.Approved.Time.UTC().Format(time.RFC3339))
	}
	if run.WaitReason != "" {
		fmt.Fprintf(&b, "waiting: %s\n", run.WaitReason)
	}
	agents := map[string]bool{}
	for agent := range run.Spend.WeeklyPct {
		agents[agent] = true
	}
	for agent := range run.Spend.Windows {
		agents[agent] = true
	}
	for _, agent := range slices.Sorted(maps.Keys(agents)) {
		fmt.Fprintf(&b, "spend: %s weekly %.1f%%%s", agent, run.Spend.WeeklyPct[agent], capText(p.CapWeekly))
		// A window that has reset holds none of this run's spend.
		if s, ok := run.Spend.Windows[agent]; ok && now.Before(s.ResetsAt) {
			fmt.Fprintf(&b, ", five-hour %.1f%%%s until %s", run.Spend.Own(agent, now), capText(p.CapWindow), s.ResetsAt.UTC().Format(time.RFC3339))
		}
		b.WriteString("\n")
	}
	if !run.Pause.IsZero() {
		if now.Before(run.Pause) {
			fmt.Fprintf(&b, "paused until %s\n", run.Pause.UTC().Format(time.RFC3339))
		} else {
			fmt.Fprintf(&b, "pause ended at %s\n", run.Pause.UTC().Format(time.RFC3339))
		}
	}
	if run.Stopped != "" {
		fmt.Fprintf(&b, "stopped: %s\n", run.Stopped)
	}
	if next := nextCommand(run); next != "" {
		fmt.Fprintf(&b, "next: duolog calibrate %s\n", next)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func capText(c float64) string {
	if c <= 0 {
		return ""
	}
	return " of cap " + number(c) + "%"
}

// nextCommand is resume for an approved run that has started and has cells
// left, plan for one that stopped for a new approval, and else the command
// after the run's step.
func nextCommand(run *Run) string {
	if run.Stopped == stopReapprove {
		return "plan"
	}
	if run.Step != "plan" || run.Approved == nil {
		return NextStep[run.Step]
	}
	started := run.Stopped != "" || !run.Pause.IsZero()
	for _, c := range run.Cells {
		if c.State != CellPending {
			started = true
			break
		}
	}
	if started {
		return "resume"
	}
	return "run"
}

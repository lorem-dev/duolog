package calibration

import (
	"context"
	"errors"
	"fmt"
)

var errWorkerHeld = errors.New("earlier worker still holds its reservation")

func (r *Runner) resumeCell(ctx context.Context, i int) error {
	if r.Accounts != nil {
		if err := r.Accounts.Reconcile(ctx); err != nil {
			return err
		}
	}
	held, err := r.stillHeld(ctx, i)
	if err != nil {
		return err
	}
	if held {
		if _, err := fmt.Fprintf(r.Out, "cell %s: an earlier attempt still holds its account reservation; resume once its worker ends\n", r.Run.Cells[i].ID); err != nil {
			return err
		}
		return errWorkerHeld
	}
	return r.removeTree(i)
}

func (r *Runner) resumeError(err error) (Outcome, error) {
	if errors.Is(err, errWorkerHeld) {
		return r.stop(stopHeld)
	}
	return Outcome{}, err
}

// USD admission is independent of whether the account has quota windows.
func (r *Runner) settleStartedUSD(started bool, agent string, held, measured float64) {
	if started {
		r.settleUSD(agent, held, measured, false, true)
	}
}

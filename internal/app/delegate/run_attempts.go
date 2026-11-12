package delegate

import (
	"context"
	"fmt"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// runAttempts keeps a step's budget, usage and output paths across retries.
// Per-attempt settlement, telemetry and learning still see each run alone.
type runAttempts struct {
	opts        Options
	usage       run.Usage
	reported    float64
	hasReported bool
	base, last  string
	unsettled   bool
}

func newRunAttempts(opts Options) *runAttempts {
	if opts.Budget != nil {
		budget := *opts.Budget
		opts.Budget = &budget
	}
	return &runAttempts{opts: opts}
}

func (a *runAttempts) run(ctx context.Context, spec run.Spec, worker func(context.Context, run.Spec) (run.Result, error)) (run.Result, error) {
	if a.base == "" {
		a.base = spec.TaskDir
	} else {
		dir, err := newAttemptDir(a.base)
		if err != nil {
			return run.Result{}, err
		}
		spec.TaskDir = dir
	}
	a.last = spec.TaskDir
	result, err := worker(ctx, spec)
	a.usage.Add(result.Usage)
	if result.ReportedUSD != nil {
		a.reported += *result.ReportedUSD
		a.hasReported = true
	}
	return result, err
}

func (a *runAttempts) settle(ctx context.Context, permit *permitRun, cfg *config.Config, route coreroute.Route, result run.Result) error {
	a.unsettled = false
	if err := permit.finish(ctx, cfg, route, result); err != nil {
		a.unsettled = true
		return err
	}
	if a.opts.Budget != nil {
		a.opts.Budget.USDRemaining = max(0, a.opts.Budget.USDRemaining-permit.chargedUSD)
	}
	return nil
}

func (a *runAttempts) stopUnsettled(err error, result run.Result) (bool, error) {
	if !a.unsettled {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if result.QuotaEnded {
		return true, fmt.Errorf("quota ended")
	}
	return false, nil
}

// complete runs in the existing finalizer, after the result line is built.
func (a *runAttempts) complete(out *Outcome, started bool) bool {
	out.Result.Usage = a.usage
	if a.hasReported {
		out.Result.ReportedUSD = &a.reported
	}
	if a.base != "" && a.last != a.base {
		out.Line = strings.ReplaceAll(out.Line, answerPointer(a.base), answerPointer(a.last))
	}
	return started
}

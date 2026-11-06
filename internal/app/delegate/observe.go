package delegate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	corerecord "github.com/lorem-dev/duolog/internal/core/record"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// Attempt and Observer live in core/record. These names keep delegate.go and
// peer.go unchanged: both are over the file-length limit, and an import line
// would grow them. They go when those files split.
type (
	Attempt  = corerecord.Attempt
	Observer = corerecord.Observer
)

// attempts numbers the worker runs of one delegation and reports each one.
// Preflight checks, routing and a run here line are not runs. run is the
// delegation's id: 16 random bytes in hex, drawn by duolog and never taken
// from a vendor.
type attempts struct {
	observe                    Observer
	stateDir, role, class, run string
	escalated                  bool
	n                          int
}

func newAttempts(opts Options, stateDir, role string, lr *learnRun) *attempts {
	a := &attempts{observe: opts.Observe, stateDir: stateDir, role: role, class: opts.Class, escalated: opts.EscalateFrom != "", run: NewRunID()}
	if lr != nil {
		a.class = lr.rec.Class
	}
	return a
}

// NewRunID draws a run id in the form telemetry keeps: a delegation's, or a
// peer launch's.
func NewRunID() string {
	b := make([]byte, 16)
	rand.Read(b) // never fails
	return hex.EncodeToString(b)
}

// workerRun is one runWorker call: when it started and how much of the gate
// log was there before it.
type workerRun struct {
	started   time.Time
	decisions int64
}

func (a *attempts) begin() workerRun {
	if a.observe == nil {
		return workerRun{}
	}
	w := workerRun{started: time.Now()}
	if info, err := os.Stat(filepath.Join(a.stateDir, "decisions.ndjson")); err == nil {
		w.decisions = info.Size()
	}
	return w
}

// worker reports a finished runWorker call. retry marks the run again after a
// login that worked.
func (a *attempts) worker(w workerRun, route coreroute.Route, retry bool, r run.Result, err error) {
	if a.observe == nil {
		return
	}
	a.n++
	// The gate counts of learning, from this run's part of the gate log. A log
	// that cannot be read counts nothing.
	denials, asks, _ := (&learnRun{stateDir: a.stateDir, role: a.role, decisions: w.decisions}).gateCounts()
	outcome, code := workerOutcome(r, err)
	if code == run.CodeNone && !r.Done && denials > 0 {
		code = run.CodeGate
	}
	reason := "none"
	switch {
	case retry:
		reason = "auth"
	case route.IsFallback:
		reason = "availability"
	}
	a.observe(Attempt{
		Number: a.n, Run: a.run, Role: a.role, Class: a.class,
		Agent: route.Agent, Family: coreroute.Normalize(route.Agent, route.Model), Effort: route.Effort, Placement: "worker",
		Usage: r.Usage, UsageKnown: r.UsageKnown, Duration: time.Since(w.started),
		Outcome: outcome, ErrorCode: code, Reason: reason,
		Asks: int64(asks), Denials: int64(denials), Escalated: a.escalated, Moved: route.IsFallback,
	})
}

// workerOutcome reads the outcome of one run and its error code. The runner
// sets the code; a run that came back without one is read from its typed
// flags and errors, never from an error's text.
func workerOutcome(r run.Result, err error) (outcome, code string) {
	code = r.ErrorCode
	if code == "" {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			code = run.CodeTimeout
		case errors.Is(err, context.Canceled):
			code = run.CodeNone
		case r.QuotaEnded:
			code = run.CodeQuota
		case r.AuthFailed:
			code = run.CodeAuth
		case err == nil:
			code = run.CodeNone
		default:
			code = run.CodeExit
		}
	}
	switch {
	case r.Interrupted:
		return "interrupted", code
	case r.QuotaEnded:
		return "infra", code
	case err != nil, r.AuthFailed:
		return "failed", code
	case len(r.Needs) > 0:
		return "needs", code
	case r.Done:
		return "done", code
	}
	return "failed", code
}

// ObserveHere reports a run here task once the lead has finished it, with the
// route Run gave and how it ended. Run reports nothing for a run here line,
// which only says where the task goes: the caller that sees the lead finish
// calls this. Placement is self when the router kept the task in the lead's
// own model, inlead when it moved to the lead's agent. The lead's usage is
// not known per task.
func ObserveHere(observe Observer, role, class string, route coreroute.Route, outcome string, d time.Duration) {
	if observe == nil {
		return
	}
	placement, reason := "inlead", "none"
	if route.Self {
		placement = "self"
	}
	if route.IsFallback {
		reason = "availability"
	}
	observe(Attempt{
		Number: 1, Role: role, Class: class,
		Agent: route.Agent, Family: coreroute.Normalize(route.Agent, route.Model), Effort: route.Effort, Placement: placement,
		Duration: d, Outcome: outcome, ErrorCode: run.CodeNone, Reason: reason, Moved: route.IsFallback,
	})
}

package delegatetest

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// Worker is a delegate.Worker that starts nothing. It records each Spec,
// calls Started with this process's id, and returns the Fake's Result.
type Worker struct {
	f      *Fake
	launch func(run.Spec) []string
	// Err, when set, is what Run returns beside Result.
	Err error
}

// Run records s and returns the scripted result.
func (w *Worker) Run(_ context.Context, s run.Spec) (run.Result, error) {
	w.f.mu.Lock()
	w.f.Calls = append(w.f.Calls, s)
	w.f.mu.Unlock()
	if s.Started != nil {
		if err := s.Started(os.Getpid()); err != nil {
			return run.Result{}, err
		}
	}
	return w.f.Result, w.Err
}

// Launch gives the words of the agent the Worker stands for.
func (w *Worker) Launch(s run.Spec) []string { return w.launch(s) }

// Runner is the shape of a delegate.Worker that the contract needs. An agent's
// tests name it here, as they may not import the app package that owns Worker.
type Runner interface {
	Run(ctx context.Context, s run.Spec) (run.Result, error)
	Launch(s run.Spec) []string
}

var _ Runner = delegate.Worker(nil)

// WorkerCase names one case of WorkerContract.
type WorkerCase string

// The cases of WorkerContract.
const (
	CaseUnknownModel  WorkerCase = "unknown model"
	CaseMissingBinary WorkerCase = "binary missing"
	CaseNoAnswer      WorkerCase = "no answer"
	CaseDone          WorkerCase = "done"
	CaseQuota         WorkerCase = "quota"
	CaseLogin         WorkerCase = "login"
)

// WorkerContract runs the cases every delegate.Worker meets. newWorker
// prepares the worker and a spec for one case: an unknown model is a spec with
// no model; a missing binary is a process whose PATH has none; no answer is a
// run that exits 1 and leaves no answer.json; done exits 0 with a valid
// answer; quota prints a usage limit with a reset time; login prints a refused
// login.
func WorkerContract(t *testing.T, newWorker func(t *testing.T, c WorkerCase) (Runner, run.Spec)) {
	t.Helper()
	t.Run(string(CaseUnknownModel), func(t *testing.T) {
		w, s := newWorker(t, CaseUnknownModel)
		if _, err := w.Run(context.Background(), s); err == nil || !strings.Contains(err.Error(), "missing model") {
			t.Fatalf("error %v, want missing model", err)
		}
	})
	t.Run(string(CaseMissingBinary), func(t *testing.T) {
		w, s := newWorker(t, CaseMissingBinary)
		if _, err := w.Run(context.Background(), s); !errors.Is(err, run.ErrBinaryMissing) {
			t.Fatalf("error %v, want run.ErrBinaryMissing", err)
		}
	})
	t.Run(string(CaseNoAnswer), func(t *testing.T) {
		w, s := newWorker(t, CaseNoAnswer)
		r, err := w.Run(context.Background(), s)
		if err == nil || r.Done || r.ErrorCode != run.CodeExit {
			t.Fatalf("%+v %v, want not done with code exit", r, err)
		}
	})
	t.Run(string(CaseDone), func(t *testing.T) {
		w, s := newWorker(t, CaseDone)
		r, err := w.Run(context.Background(), s)
		if err != nil || !r.Done {
			t.Fatalf("%+v %v, want done", r, err)
		}
	})
	t.Run(string(CaseQuota), func(t *testing.T) {
		w, s := newWorker(t, CaseQuota)
		r, _ := w.Run(context.Background(), s)
		if r.ErrorCode != run.CodeQuota || !r.QuotaEnded || r.ResetAt.Before(time.Now()) {
			t.Fatalf("%+v, want code quota with a reset time ahead", r)
		}
	})
	t.Run(string(CaseLogin), func(t *testing.T) {
		w, s := newWorker(t, CaseLogin)
		r, _ := w.Run(context.Background(), s)
		if r.ErrorCode != run.CodeAuth || !r.AuthFailed {
			t.Fatalf("%+v, want code auth", r)
		}
	})
}

package delegatetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/run"
)

// TestWorkerContract runs the contract against the fake, scripted per case.
func TestWorkerContract(t *testing.T) {
	WorkerContract(t, func(t *testing.T, c WorkerCase) (Runner, run.Spec) {
		f := New(t)
		w := f.Deps().Agents["claude"].Worker.(*Worker)
		switch c {
		case CaseUnknownModel:
			w.Err = errors.New("worker failed: missing model")
		case CaseMissingBinary:
			w.Err = errors.Join(errors.New("not found"), run.ErrBinaryMissing)
		case CaseNoAnswer:
			f.Result, w.Err = run.Result{ErrorCode: run.CodeExit}, errors.New("exit 1")
		case CaseQuota:
			f.Result = run.Result{ErrorCode: run.CodeQuota, QuotaEnded: true, ResetAt: time.Now().Add(time.Hour)}
		case CaseLogin:
			f.Result = run.Result{ErrorCode: run.CodeAuth, AuthFailed: true}
		}
		return w, run.Spec{}
	})
}

func TestFakeWorkerRecordsCalls(t *testing.T) {
	f := New(t)
	w := f.Deps().Agents["codex"].Worker
	if _, err := w.Run(context.Background(), run.Spec{Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 1 || f.Calls[0].Prompt != "x" || len(w.Launch(run.Spec{})) == 0 {
		t.Fatalf("calls %+v", f.Calls)
	}
}

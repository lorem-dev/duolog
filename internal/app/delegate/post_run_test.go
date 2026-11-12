package delegate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/run"
)

func TestPostRunAccountingKeepsWorkerOutcome(t *testing.T) {
	workerErr := errors.New("worker failed")
	for _, tc := range []struct {
		name, state string
		result      run.Result
		err         error
	}{
		{"done", "done", run.Result{Done: true, Summary: "Finished", Files: []string{"a.go"}}, nil},
		{"needs", "needs", run.Result{Summary: "Needs a tool", Needs: []string{"mcp:example"}}, nil},
		{"failed", "failed", run.Result{}, workerErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.results, h.errs = []run.Result{tc.result}, []error{tc.err}
			worker := h.worker()
			statePath := filepath.Join(h.gate.Store.Dir, "state.json")
			var saved []byte
			h.setWorker(func(ctx context.Context, spec run.Spec) (run.Result, error) {
				result, err := worker(ctx, spec)
				saved, _ = os.ReadFile(statePath)
				put(t, statePath, "invalid account state")
				return result, err
			})
			out, err := h.run("docs")
			if !errors.Is(err, tc.err) || tc.err == nil && err != nil {
				t.Fatalf("worker error changed: %v", err)
			}
			record := RecordFor(Record{}, out, err, time.Now())
			if record.State != tc.state || !strings.Contains(record.Line, "accounting failed") {
				t.Fatalf("worker outcome lost: %+v", record)
			}
			if err == nil && (record.Done != tc.result.Done || record.Summary != tc.result.Summary || !slices.Equal(record.Files, tc.result.Files)) {
				t.Fatalf("worker answer lost: %+v", record)
			}
			put(t, statePath, string(saved))
			// The next admission replays the measured settlement even while
			// this supervisor still runs, rather than charging an estimate.
			if err := h.gate.Store.Reconcile(context.Background(), paidruns.ReconcileInput{}); err != nil {
				t.Fatal(err)
			}
			if st := h.accounts(); len(st.Reservations) != 0 || len(st.Settled) != 1 {
				t.Fatalf("settlement was not recovered: %+v", st)
			}
		})
	}
}

func TestUsageFailureKeepsWorkerAnswer(t *testing.T) {
	h := newHarness(t)
	if err := os.Mkdir(filepath.Join(h.s.Dir, "usage.ndjson"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := h.run("docs")
	record := RecordFor(Record{}, out, err, time.Now())
	if err != nil || record.State != "done" || !record.Done || !strings.Contains(record.Line, "usage accounting failed") {
		t.Fatalf("usage failure hid the answer: %+v (%v)", record, err)
	}
}

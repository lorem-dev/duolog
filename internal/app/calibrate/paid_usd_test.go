package calibration

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// TestPaidCellUSDCountsInterruptedAttempts is review finding I4, first half:
// a paid cell whose worker started and whose attempt then broke off, as when
// the supervisor dies, keeps its high USD estimate in the run's paid spend,
// saved with the running cell; a resume is a new attempt on top of it. A
// finished cell's charge is corrected to what it cost.
func TestPaidCellUSDCountsInterruptedAttempts(t *testing.T) {
	f := newRunFixture(t, []string{"t1"}, []string{"codex/sol/medium"})
	f.runner.Billing = paidCodex(10, 100, 0)
	high := cellUSD(f.runner.Cfg, "codex", "sol", runEstimate)
	if high <= 0 {
		t.Fatalf("high USD %v, want above 0", high)
	}
	crash := true
	f.runner.Cell = func(ctx context.Context, c Cell, task TaskRecord, start StartFunc) (CellResult, error) {
		if err := StartStep(start); err != nil {
			return CellResult{}, err
		}
		if crash {
			return CellResult{}, errors.New("the supervisor died")
		}
		return CellResult{State: CellDone, Passed: true, Tokens: runEstimate, USD: 0.5, Seconds: 1}, nil
	}
	if _, err := f.runner.Execute(context.Background()); err == nil {
		t.Fatal("a crashed cell succeeded")
	}
	saved := f.saved(t)
	if got := saved.Spend.PaidUSD["codex"]; math.Abs(got-high) > 1e-9 || saved.Cells[0].State != CellRunning {
		t.Fatalf("after an interrupted attempt: paid %v state %s, want %v and running", got, saved.Cells[0].State, high)
	}
	crash = false
	f.runner.Run = saved
	if o := f.execute(t); !o.Done {
		t.Fatalf("resume: %+v", o)
	}
	if got := f.saved(t).Spend.PaidUSD["codex"]; math.Abs(got-(high+0.5)) > 1e-9 {
		t.Fatalf("after the resume: paid %v, want the first attempt's %v and the second's 0.5", got, high)
	}
}

// TestCellUSDTakesReportedCost is review finding I4, second half: a Claude
// cell whose CLI reports a cost above its token price is charged that cost.
func TestCellUSDTakesReportedCost(t *testing.T) {
	f := newCellFixture(t)
	var out delegate.Outcome
	answer := `{"session_id":"worker-1","usage":{"input_tokens":1000,"output_tokens":200},"total_cost_usd":5,` +
		`"structured_output":{"done":true,"summary":"Added Parse.","files":["parse.go"],"needs":[]}}` + "\n"
	f.realClaude(t, testbin.Scenario{Stdout: answer, Writes: map[string]string{
		"parse.go": "package widget\n\nfunc Parse(line string) []string { return nil }\n"}}, &out)
	r := f.run(t)
	if r.USD != 5 {
		t.Fatalf("cell USD %v (%q), want the reported 5", r.USD, out.Line)
	}
}

package delegate

import (
	"context"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// A Claude run cancelled or timed out mid-request knows only its finished
// messages' usage. It settles as incomplete, so a paid run is charged at
// least its estimate for the request in flight.
func TestInterruptedRunSettlesAsIncomplete(t *testing.T) {
	h := newHarness(t)
	h.results = []run.Result{{Interrupted: true, Usage: run.Usage{Input: 50, Output: 5}, UsageKnown: true}}
	h.errs = []error{context.Canceled}
	if _, err := h.run("implement"); err == nil {
		t.Fatal("a cancelled run succeeded")
	}
	if s := h.wantSettledOnce(1)[0]; s.CompleteUsage {
		t.Fatalf("a run without its total usage settled as complete: %+v", s)
	}
}

// The refusal line gives the paid_runs advice only to an agent that has not
// opted in, and names a pane as a peer.
func TestRefusalLineWording(t *testing.T) {
	signals := []workerenv.Signal{{Name: "env:ANTHROPIC_BASE_URL", Layer: "user"}}
	refusal := func(shape workerenv.Shape, optedIn bool) *paidruns.Refusal {
		return &paidruns.Refusal{Agent: "claude", Shape: shape, Variant: workerenv.Free,
			Reason: paidruns.ReasonFreeVariantPaid, Verdict: paidruns.Paid, Signals: signals, OptedIn: optedIn}
	}
	if got := RefusalLine(refusal(workerenv.Worker, false)); got != "claude worker is paid (env:ANTHROPIC_BASE_URL in user); set paid_runs.claude to allow" {
		t.Fatalf("not opted in: %q", got)
	}
	if got := RefusalLine(refusal(workerenv.Worker, true)); got != "claude worker is paid (env:ANTHROPIC_BASE_URL in user)" {
		t.Fatalf("opted in: %q", got)
	}
	if got := RefusalLine(refusal(workerenv.Peer, false)); !strings.HasPrefix(got, "claude peer is paid") {
		t.Fatalf("pane: %q", got)
	}
}

// With paid runs allowed and the cap too small, the lead line says the free
// worker is paid and the cap is reached, without advice to allow what is.
func TestLeadLineOmitsAllowAdviceWhenOptedIn(t *testing.T) {
	h := newHarness(t)
	h.paidReads("codex", "claude")
	h.cfg.PaidRuns.Codex.Allow, h.cfg.PaidRuns.Codex.DailyCapUSD = true, 0.0001
	o, err := h.run("implement")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(o.Line, "codex worker is paid") || !strings.Contains(o.Line, "codex daily cap reached") ||
		strings.Contains(o.Line, "set paid_runs.codex to allow") {
		t.Fatalf("lead line: %q", o.Line)
	}
}

// A free run that spent paid overage says so in its line.
func TestFreeRunOnOverageSaysSo(t *testing.T) {
	h := newHarness(t)
	h.results = []run.Result{{Done: true, Summary: "Completed.", Files: []string{}, Needs: []string{},
		Usage: run.Usage{Input: 12, Output: 3}, UsageKnown: true, UsageComplete: true,
		Billing: quota.RunEvidence{Expected: true, Seen: true, UsingOverage: true}}}
	o, err := h.run("implement")
	if err != nil {
		t.Fatal(err)
	}
	want := h.calls[0].Route.Agent + " worker ran on paid overage; duolog stopped further runs"
	if !strings.Contains(o.Line, want) {
		t.Fatalf("line %q lacks %q", o.Line, want)
	}
}

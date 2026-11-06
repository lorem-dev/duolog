package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/calibration"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// readsGate makes delegate.NewGate answer every quota read as read does,
// over a temporary account state, and counts the reads.
func readsGate(t *testing.T, read func(context.Context, workerenv.Process) (quota.Snapshot, error)) *int {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "accounts")
	reads := new(int)
	old := delegate.NewGate
	delegate.NewGate = func(cfg *config.Config) (*paidruns.Gate, error) {
		g, err := paidrunstest.Gate(dir, cfg)
		if err != nil {
			return nil, err
		}
		g.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
			*reads++
			return read(ctx, p)
		}
		return g, nil
	}
	t.Cleanup(func() { delegate.NewGate = old })
	return reads
}

// paidClaudeReads answers Claude on an API key route and Codex as a
// subscription.
func paidClaudeReads(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
	s, err := paidrunstest.Subscription(ctx, p)
	if p.Agent == "claude" {
		s.Billing.RouteSignals = []workerenv.Signal{{Name: "auth:apiKey", Layer: "auth status"}}
	}
	return s, err
}

func writeCalibrationConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.local.yaml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// duolog calibrate plan reads each agent's billing through the account gate
// and costs a paid route in the plan: without paid_runs.<agent>.allow and
// calibrate the plan is refused; with both it shows the USD estimate, the
// cap and the price caveat, and approves as any plan does.
func TestCalibrationPlanPaidCommand(t *testing.T) {
	root := learningRepo(t, true)
	fakeQuota(t)
	minedRun(t, filepath.Join(root, ".duolog"))
	reads := readsGate(t, paidClaudeReads)

	_, err := executeBusCommand(t, "calibrate", "plan")
	if err == nil || !strings.Contains(err.Error(), "paid_runs.claude.calibrate") {
		t.Fatalf("a paid route without the calibrate opt-in: %v", err)
	}
	if *reads == 0 {
		t.Fatal("the plan did not read billing through the gate")
	}

	writeCalibrationConfig(t, root, "paid_runs:\n  claude:\n    allow: true\n    daily_cap_usd: 500\n    calibrate: true\n")
	out, err := executeBusCommand(t, "calibrate", "plan")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"paid claude:", "USD high estimate", "of the 500 USD cap", "prices are a lower bound"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	const prefix = "approve with: duolog calibrate plan --approve "
	i := strings.Index(out, prefix)
	if i < 0 {
		t.Fatalf("no approval line:\n%s", out)
	}
	hash := strings.TrimSpace(strings.SplitN(out[i+len(prefix):], "\n", 2)[0])
	if out, err := executeBusCommand(t, "calibrate", "plan", "--approve", hash); err != nil {
		t.Fatal(out, err)
	}
	stateDir := filepath.Join(root, ".duolog")
	run, ok, err := calibration.LatestRun(stateDir)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	p, err := calibration.LoadPlan(run.Dir(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if b := p.Billing["claude"]; b.Verdict != calibration.VerdictPaid || b.ApprovedUSD <= 0 || !b.Allow || !b.Calibrate {
		t.Fatalf("approved billing: %+v", p.Billing)
	}
}

// Calibration reads quota through the account gate's route-aware cache: the
// worker's free read, its percentages taken once as they are, a rejected
// family window mapped to full, and a fresh read when asked for one.
func TestCalibrationUsableShareQuotaThroughGate(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	reads := readsGate(t, func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
		s, err := paidrunstest.Subscription(ctx, p)
		s.Windows[0].UsedPercent, s.Windows[1].UsedPercent = 30, 40
		s.ModelLimits = map[string][]quota.Window{"sol": {{Kind: "session", DurationMinutes: 300, UsedPercent: 20,
			ResetsAt: time.Now().Add(time.Hour), Severity: "rejected"}}}
		return s, err
	})
	cfg, err := config.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	read := newCalibrationAccounts(cfg, ".").Quota()
	ws, err := read.Get(context.Background(), "codex", false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"five_hour/": 30, "weekly/": 40, "five_hour/sol": 100}
	if len(ws) != len(want) {
		t.Fatalf("windows: %+v", ws)
	}
	for _, w := range ws {
		if got, ok := want[w.Kind+"/"+w.Model]; !ok || got != w.UsedPercent {
			t.Fatalf("window %+v, want %v", w, want)
		}
	}
	before := *reads
	if _, err := read.Get(context.Background(), "codex", false); err != nil || *reads != before {
		t.Fatalf("a cached read read again: %d then %d (%v)", before, *reads, err)
	}
	if _, err := read.Get(context.Background(), "codex", true); err != nil || *reads != before+1 {
		t.Fatalf("a fresh read did not read: %d then %d (%v)", before, *reads, err)
	}
}

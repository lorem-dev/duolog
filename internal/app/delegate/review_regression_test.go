package delegate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/core/run"
)

func TestSnapshotHashesLinkText(t *testing.T) {
	// Kills following untracked links into blocking or unbounded targets.
	h := newHarness(t)
	h.gitRepo()
	outside := filepath.Join(t.TempDir(), "target")
	put(t, outside, "first")
	link := filepath.Join(h.root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}
	before, err := snapshot(h.root)
	if err != nil {
		t.Fatal(err)
	}
	put(t, outside, "changed target")
	after, err := snapshot(h.root)
	if err != nil {
		t.Fatal(err)
	}
	if before["link"] != after["link"] {
		t.Fatal("snapshot followed the link")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside+"-missing", link); err != nil {
		t.Fatal(err)
	}
	after, err = snapshot(h.root)
	if err != nil {
		t.Fatal(err)
	}
	if before["link"] == after["link"] {
		t.Fatal("changed link text was missed")
	}
}

func TestLoginRetryKeepsDistinctLogsAndAggregateUsage(t *testing.T) {
	// Kills reuse of the truncated log and loss of the first attempt's usage.
	h := newHarness(t)
	h.results = []run.Result{{AuthFailed: true, Usage: run.Usage{Input: 100, Cached: 40, Output: 10}}}
	h.auth.login = LoggedIn
	o, err := h.run("implement")
	if err != nil {
		t.Fatal(err)
	}
	h.requireCalls(2)
	if h.calls[0].TaskDir == h.calls[1].TaskDir {
		t.Error("retry reused its log directory")
	}
	if o.Result.Usage.Input != 112 || o.Result.Usage.Output != 13 {
		t.Fatalf("usage %+v", o.Result.Usage)
	}
}

func TestLoginRetryUsesRemainingStepBudget(t *testing.T) {
	// Kills admitting each login retry against the original calibration budget.
	h := authHarness(t)
	h.cfg.Routing.SmallTaskFiles = 0
	pa := h.cfg.PaidRuns.Agent("codex")
	pa.Allow, pa.Calibrate, pa.DailyCapUSD = true, true, 100
	h.cfg.PaidRuns.Set("codex", pa)
	h.paidReads("codex")
	// Set the cold estimate to one dollar.
	price := h.cfg.Models["sol"]
	price.Input, price.Output, price.Cached = 2.0/3, 2.0/3, 0
	h.cfg.Models["sol"] = price
	usd := 1.0
	h.results = []run.Result{{AuthFailed: true, Usage: run.Usage{Input: 1_000_000}, UsageKnown: true, UsageComplete: true, ReportedUSD: &usd}}
	h.auth.login = LoggedIn
	e := *target("codex/sol/high")
	budget := &paidrules.StepBudget{PlanHash: "plan", StepID: "cell.1", USDRemaining: 1.5, WeeklyRemaining: 1, WindowRemaining: 1}
	_, _ = h.runWith("implement", Options{Calibration: true, Entry: &e, Budget: budget})
	if len(h.calls) > 1 {
		t.Fatalf("retry overspent the step: %d calls", len(h.calls))
	}
	if len(h.calls) == 0 {
		t.Fatal("first attempt never ran")
	}
	if budget.USDRemaining != 1.5 {
		t.Fatal("caller budget mutated")
	}
}

func TestFallbackKeepsDistinctLogs(t *testing.T) {
	// Kills truncating an earlier candidate's log when a quota fallback starts.
	h := newHarness(t)
	h.limited()
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(2)
	if h.calls[0].TaskDir == h.calls[1].TaskDir {
		t.Fatal("fallback reused its log directory")
	}
}

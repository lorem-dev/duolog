package quota_test

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/app/quota"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/budget"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// A switched-off agent's quota and budget are not read, and Budgets gives it
// none; the agents that are on are read.
func TestBudgetsSkipsASwitchedOffAgent(t *testing.T) {
	on, off := true, false
	cfg := &config.Config{EnvPassthrough: config.DefaultEnvPassthrough(runtime.GOOS),
		Agents: map[string]config.AgentSwitch{"claude": {Enabled: &on}, "codex": {Enabled: &off}}}
	cfg.PaidRuns.Set("codex", config.PaidAgent{Account: "auto", Allow: true, Calibrate: true, DailyCapUSD: 5})
	g, err := paidrunstest.Gate(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var read []string
	g.Cache = paidrunstest.Cache(func(ctx context.Context, p launch.Process) (account.Snapshot, error) {
		mu.Lock()
		read = append(read, p.Agent)
		mu.Unlock()
		return paidrunstest.Subscription(ctx, p)
	})
	a := quota.NewAccounts(quota.Deps{}, func() (*paidruns.Gate, error) { return g, nil }, cfg, t.TempDir())
	budgets, err := a.Budgets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(read, "codex") || !slices.Contains(read, "claude") {
		t.Errorf("Budgets read %q, want claude and not the switched-off codex", read)
	}
	if _, ok := budgets["codex"]; ok {
		t.Errorf("budgets = %+v, want none for codex", budgets)
	}
	if b, ok := budgets["claude"]; !ok || b.Verdict != budget.VerdictSubscription {
		t.Errorf("budgets = %+v, want claude's subscription", budgets)
	}
}

// A Codex individual limit is calibration's weekly window when the read has
// no weekly window, so its lead reserve and cap hold calibration back as the
// limit nears full; next to a weekly window it is left out.
func TestWindowsMapsAnIndividualLimit(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	reset := now.Add(80 * time.Hour)
	individual := account.Window{Kind: "individual", UsedPercent: 42, ResetsAt: reset}
	ws := quota.Windows(account.Snapshot{Windows: []account.Window{individual}})
	if len(ws) != 1 || ws[0] != (budget.Window{Kind: budget.KindWeekly, UsedPercent: 42, ResetsAt: reset}) {
		t.Fatalf("windows = %+v", ws)
	}
	cfg := config.Calibration{MaxWeeklyShare: 0.10, MaxWindowShare: 0.5}
	spend := budget.Spend{WeeklyPct: map[string]float64{}}
	cost := budget.CellCost{WeeklyPct: 1, WindowPct: 5}
	if d := budget.AdmitBilled("codex", budget.BillingBudget{Verdict: budget.VerdictSubscription, Account: "workspace", UsableShare: 0.5}, ws, cost, spend, cfg, 0, now); d.Stop != "lead reserve" {
		t.Fatalf("42%% of a workspace individual limit, above (100-15)*0.5: %+v, want the lead reserve", d)
	}
	if d := budget.AdmitBilled("codex", budget.BillingBudget{Verdict: budget.VerdictSubscription, Account: "personal", UsableShare: 1}, ws, cost, spend, cfg, 0, now); !d.Admit {
		t.Fatalf("42%% of a personal individual limit: %+v", d)
	}
	weekly := account.Window{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 20, ResetsAt: reset}
	ws = quota.Windows(account.Snapshot{Windows: []account.Window{weekly, individual}})
	if len(ws) != 1 || ws[0].UsedPercent != 20 {
		t.Fatalf("windows next to a weekly one = %+v", ws)
	}
}

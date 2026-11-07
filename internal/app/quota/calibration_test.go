package quota_test

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"testing"

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

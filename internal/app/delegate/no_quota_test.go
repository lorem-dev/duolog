package delegate

import (
	"context"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/quota"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// readFunc is a quota.Reader made of a read function.
type readFunc func(context.Context, corelaunch.Process) (account.Snapshot, error)

func (f readFunc) Read(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
	return f(ctx, p)
}

// An agent with no Quota fails closed. The gate reads through the agents'
// readers, as wiring builds it, so the agent is never read: paidrules.Evaluate
// makes its worker paid with read:failed. Though paid_runs.codex allows paid
// runs with a cap, it gets no paid pass, and the task goes to the lead with
// the refusal.
func TestNoQuotaNoPaidPass(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Ask, "high")
	h.cfg.PaidRuns.Set("codex", config.PaidAgent{Allow: true, DailyCapUSD: 5})
	codex := h.deps.Agents["codex"]
	codex.Quota = nil
	h.deps.Agents["codex"] = codex
	readers := quota.Deps{Agents: map[string]quota.Reader{}}
	for name, a := range h.deps.Agents {
		if a.Quota != nil {
			readers.Agents[name] = readFunc(h.gate.Cache.Read)
		}
	}
	h.gate.Cache.Read = readers.Read

	o, err := h.run("implement")
	if err != nil {
		t.Fatal(err)
	}
	h.requireCalls(0)
	if o.RunHere == nil || !strings.HasPrefix(o.Line, "implement -> lead: codex worker is paid") {
		t.Fatalf("lead line: %q", o.Line)
	}
	if !strings.Contains(o.Line, "read:failed") {
		t.Fatalf("the line does not name the failed read: %q", o.Line)
	}
	if strings.Contains(o.Line, "daily cap") || strings.Contains(o.Line, "paid variant") {
		t.Fatalf("a paid pass ran for an agent without a quota reader: %q", o.Line)
	}
}

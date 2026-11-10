package peer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/account"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func fakePace(t *testing.T, week map[string]float64) {
	t.Helper()
	old := paidruns.PaceRead
	t.Cleanup(func() { paidruns.PaceRead = old })
	yes := func() *bool { v := true; return &v }
	paidruns.PaceRead = func(_ context.Context, p corelaunch.Process) (account.Snapshot, error) {
		used, ok := week[p.Agent]
		if !ok {
			return account.Snapshot{}, context.DeadlineExceeded
		}
		now := time.Now()
		s := account.Snapshot{ReadAt: now, Windows: []account.Window{
			{Kind: "session", DurationMinutes: 300, UsedPercent: 10, ResetsAt: now.Add(time.Hour)},
			{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: used * 100, ResetsAt: now.Add(338688 * time.Second)},
		}}
		if p.Agent == "claude" {
			s.Billing = account.BillingFacts{AuthMethod: "claude.ai", APIProvider: "firstParty", RouteKnown: true, WindowsAvailable: yes()}
		} else {
			s.Billing = account.BillingFacts{AccountType: "chatgpt", AuthMode: "chatgpt", RouteKnown: true, OrdinaryUsageAllowed: yes()}
		}
		return s, nil
	}
}

func TestOpenReportsPaceReason(t *testing.T) {
	// Kills a pace-routing test that waits for Codex output from a Claude pane.
	r := newRig(t, withScreens(claudeIdle("")))
	fakePace(t, map[string]float64{"codex": .61, "claude": .30})
	e := r.open("implement")
	if e.State != Ready || !strings.Contains(e.Note, "implement -> claude sonnet: codex 61% of the week at 44% elapsed") {
		t.Fatalf("%+v", e)
	}
}

// A balanced move onto the lead's own agent opens no pane; its note still
// carries the reason.
func TestOpenInLeadReportsPaceReason(t *testing.T) {
	r := newRig(t, baseScript())
	r.lead("claude", mode.Ask)
	fakePace(t, map[string]float64{"codex": .61, "claude": .30})
	e := r.open("implement")
	if e.State != Limited || !strings.Contains(e.Note, "subagent") ||
		!strings.HasSuffix(e.Note, "implement -> claude sonnet: codex 61% of the week at 44% elapsed") {
		t.Fatalf("%+v", e)
	}
}

func TestOpenKeepsUseWithoutPaceReason(t *testing.T) {
	for name, week := range map[string]map[string]float64{
		"unknown":             {},
		"fallback also ahead": {"codex": .61, "claude": .61},
	} {
		r := newRig(t, baseScript())
		fakePace(t, week)
		if e := r.open("implement"); strings.Contains(e.Note, "->") {
			t.Errorf("%s: %+v", name, e)
		}
	}
}

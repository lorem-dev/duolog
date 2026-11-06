package delegate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

func fakePace(t *testing.T, week map[string]float64) {
	t.Helper()
	old := paidruns.PaceRead
	t.Cleanup(func() { paidruns.PaceRead = old })
	yes := func() *bool { v := true; return &v }
	paidruns.PaceRead = func(_ context.Context, p workerenv.Process) (quota.Snapshot, error) {
		used, ok := week[p.Agent]
		if !ok {
			return quota.Snapshot{}, context.DeadlineExceeded
		}
		now := time.Now()
		s := quota.Snapshot{ReadAt: now, Windows: []quota.Window{
			{Kind: "session", DurationMinutes: 300, UsedPercent: 10, ResetsAt: now.Add(time.Hour)},
			{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: used * 100, ResetsAt: now.Add(338688 * time.Second)},
		}}
		if p.Agent == "claude" {
			s.Billing = quota.BillingFacts{AuthMethod: "claude.ai", APIProvider: "firstParty", RouteKnown: true, WindowsAvailable: yes()}
		} else {
			s.Billing = quota.BillingFacts{AccountType: "chatgpt", AuthMode: "chatgpt", RouteKnown: true, OrdinaryUsageAllowed: yes()}
		}
		return s, nil
	}
}

func statusMessages(t *testing.T, h *harness) []bus.Message {
	t.Helper()
	var out []bus.Message
	for _, line := range strings.Split(strings.TrimSpace(read(t, filepath.Join(h.s.Dir, "bus.ndjson"))), "\n") {
		var msg bus.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatal(err)
		}
		if msg.K == "status" {
			out = append(out, msg)
		}
	}
	return out
}

func TestDelegateTellsPaceReason(t *testing.T) {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	fakePace(t, map[string]float64{"codex": .61, "claude": .30})
	if _, e := h.run("implement"); e != nil {
		t.Fatal(e)
	}
	moves := statusMessages(t, h)
	if len(moves) != 1 || !strings.HasPrefix(moves[0].S, "role implement moved from codex to claude model=sonnet effort=medium; ") ||
		!strings.HasSuffix(moves[0].S, "implement -> claude sonnet: codex 61% of the week at 44% elapsed") {
		t.Fatalf("status messages: %+v", moves)
	}
}

func TestDelegateKeepsUseWhenPaceUnknownOrFallbackAhead(t *testing.T) {
	for name, week := range map[string]map[string]float64{
		"unknown":             {},
		"fallback also ahead": {"codex": .61, "claude": .61},
	} {
		h := newHarness(t)
		h.lead("claude", mode.Edits, "high")
		fakePace(t, week)
		if _, e := h.run("implement"); e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		if moves := statusMessages(t, h); len(moves) != 0 {
			t.Errorf("%s: status messages: %+v", name, moves)
		}
	}
}

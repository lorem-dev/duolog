package delegate

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestKnownPlacementSkipsAccountAdmission(t *testing.T) {
	for _, scenario := range []string{"self", "in lead", "small task", "no agent"} {
		t.Run(scenario, func(t *testing.T) {
			h := newHarness(t)
			h.lead("codex", mode.Ask, "high")
			role := "plan"
			switch scenario {
			case "self":
				h.lead("claude", mode.Ask, "high")
			case "in lead":
				h.unavailable("claude")
			case "small task":
				role = "implement"
				h.cfg.Routing.SmallTaskFiles = 2
			case "no agent":
				h.unavailable("claude")
				h.unavailable("codex")
			}
			h.deps.PaidGate = func(*config.Config) (*paidruns.Gate, error) {
				t.Fatal("known placement opened the account gate")
				return nil, nil
			}
			old := paidruns.PaceRead
			t.Cleanup(func() { paidruns.PaceRead = old })
			paidruns.PaceRead = func(context.Context, corelaunch.Process) (account.Snapshot, error) {
				t.Fatal("known placement waited for quota")
				return account.Snapshot{}, nil
			}
			out, err := h.runWith(role, Options{AdmissionWait: func(reason string) {
				if reason != "" {
					t.Fatalf("known placement waited: %s", reason)
				}
			}})
			h.requireCalls(0)
			if scenario == "no agent" {
				if err == nil {
					t.Fatal("unavailable agents were accepted")
				}
			} else if err != nil || out.RunHere == nil {
				t.Fatalf("placement: %+v %v", out, err)
			}
		})
	}
}

func TestAdmissionReportsQuotaAndSessionWaits(t *testing.T) {
	h := newHarness(t)
	h.cfg.Routing.Balance = "off"
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	// Keep a live reservation occupying the only slot, then advance a fake
	// clock through the wait. The gate's admission decisions stay unchanged.
	h.cfg.Limits.MaxTotalSessions = 1
	held := h.held[0]
	held.ID = "busy-slot"
	if err := h.gate.Store.Update(context.Background(), func(st *paidruns.State) error {
		st.Reservations[held.ID] = held
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	h.gate.Now = func() time.Time { return now }
	var reason string
	var reasons []string
	var waiting []bool
	h.gate.Wait = func(context.Context, time.Duration) error {
		if !strings.Contains(reason, "session slot") {
			t.Fatalf("session queue has no reason: %q", reason)
		}
		now = now.Add(time.Minute)
		return nil
	}
	h.lead("claude", mode.Ask, "high")
	out, err := h.runWith("implement", Options{
		AdmissionWait: func(s string) { reason = s; reasons = append(reasons, s) },
		Waiting:       func(on bool) { waiting = append(waiting, on) },
	})
	if err != nil || out.RunHere == nil || reason != "" {
		t.Fatalf("after wait: %+v %v; reason %q", out, err, reason)
	}
	if !slices.Contains(reasons, "waiting for codex quota") || !slices.Contains(reasons, "waiting for a codex session slot") || !slices.Equal(waiting, []bool{true, false}) {
		t.Fatalf("wait callbacks: %q %v", reasons, waiting)
	}
}

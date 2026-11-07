package paidruns

import (
	"context"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/paidstore"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// ReportCached reads no quota: without a read in the cache the assessment is
// still the agent's, and once Report has read, ReportCached judges from that
// read and gives the same verdict and session numbers.
func TestReportCachedReadsNothing(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	in := w.request("claude", workerenv.Worker, workerenv.Free, 100).Input
	cold, _ := w.gate.ReportCached(ctx, in)
	if w.reads != 0 || cold.Assessment.Agent != "claude" {
		t.Fatalf("reads %d, report %+v", w.reads, cold.Assessment)
	}
	live, err := w.gate.Report(ctx, in)
	if err != nil || w.reads != 1 {
		t.Fatalf("reads %d: %v", w.reads, err)
	}
	cached, err := w.gate.ReportCached(ctx, in)
	if err != nil || w.reads != 1 {
		t.Fatalf("reads %d: %v", w.reads, err)
	}
	c, l := cached.Sessions, live.Sessions
	if cached.Assessment.Verdict != live.Assessment.Verdict || c.Tier != l.Tier || c.Limit != l.Limit || c.Live != l.Live {
		t.Fatalf("cached %+v %+v, live %+v %+v", cached.Assessment, cached.Sessions, live.Assessment, live.Sessions)
	}
}

// coldCache gives the gate an empty cache, as a new hook process has.
func (w *gateWorld) coldCache() {
	w.gate.Cache = &paidstore.Cache{Read: w.read, Now: func() time.Time { return w.now }}
}

// Without a read in the cache, ReportCached gives the sessions of the tier
// admission recorded last, never the small tier of its paid verdict; with no
// tier recorded, the small tier every agent starts at.
func TestReportCachedKeepsAdmissionsLastTier(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	in := w.request("claude", workerenv.Worker, workerenv.Free, 100).Input
	if cold, _ := w.gate.ReportCached(ctx, in); cold.Sessions.Tier != paidrules.TierSmall || cold.Sessions.Limit != 3 {
		t.Fatalf("no tier recorded: %+v", cold.Sessions)
	}
	if err := w.store.Update(ctx, func(st *State) error {
		st.Tiers["claude"] = paidrules.TierResult{Tier: paidrules.TierLarge, Reason: "session 0.01 share/M at or below 0.015", Limit: 10}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	report, _ := w.gate.ReportCached(ctx, in)
	if w.reads != 0 || report.Sessions.Tier != paidrules.TierLarge || report.Sessions.Limit != 10 {
		t.Fatalf("reads %d, sessions %+v, want the large tier's 10", w.reads, report.Sessions)
	}
}

// SessionWindow is the five-hour use the latest admission measured, while
// that admission is at most maxAge old and its window has not reset.
func TestSessionWindowFromTheLatestAdmission(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	if _, known, err := w.gate.SessionWindow(ctx, "claude", 30*time.Minute); err != nil || known {
		t.Fatalf("no admission: known %v, %v", known, err)
	}
	w.used = 41
	w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 100))
	w.coldCache()
	reads := w.reads
	used, known, err := w.gate.SessionWindow(ctx, "claude", 30*time.Minute)
	if err != nil || !known || used < 40.99 || used > 41.01 || w.reads != reads {
		t.Fatalf("used %g, known %v, reads %d: %v", used, known, w.reads-reads, err)
	}
	if _, known, _ := w.gate.SessionWindow(ctx, "codex", 30*time.Minute); known {
		t.Fatal("another agent's admission gave codex a window")
	}
	w.now = w.now.Add(31 * time.Minute)
	if _, known, _ := w.gate.SessionWindow(ctx, "claude", 30*time.Minute); known {
		t.Fatal("an admission 31 minutes old still gave the window")
	}
	w.now = w.now.Add(90 * time.Minute)
	if _, known, _ := w.gate.SessionWindow(ctx, "claude", 3*time.Hour); known {
		t.Fatal("a window that reset since still gave its use")
	}
}

// A settled task keeps the window its admission read: with every task
// settled 5 minutes ago and the window at 30%, the window is still 30%,
// until the admission is more than maxAge old.
func TestSessionWindowFromSettledTasks(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	w.used = 30
	worker := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 100))
	w.procs[100] = Alive
	open := w.acquire(w.peerOpenRequest("claude", workerenv.Free, 200))
	w.now = w.now.Add(5 * time.Minute)
	send := w.acquire(w.peerSend("claude", workerenv.Free, 201, open.Reservation.ID))
	w.now = w.now.Add(5 * time.Minute)
	w.finish(worker, paidrules.Settlement{ID: worker.Reservation.ID, Started: true})
	w.finish(send, paidrules.Settlement{ID: send.Reservation.ID, Started: true})
	for _, r := range w.state().Reservations {
		if r.Task {
			t.Fatalf("a task is still in flight: %+v", r)
		}
	}
	w.now = w.now.Add(5 * time.Minute)
	w.coldCache()
	used, known, err := w.gate.SessionWindow(ctx, "claude", 30*time.Minute)
	if err != nil || !known || used != 30 {
		t.Fatalf("used %g, known %v: %v; want 5h 30%%", used, known, err)
	}
	w.now = w.now.Add(21 * time.Minute)
	if _, known, _ := w.gate.SessionWindow(ctx, "claude", 30*time.Minute); known {
		t.Fatal("settled tasks admitted 31 minutes ago still gave the window")
	}
}

// Only an admission of the agent's current account gives its window: after
// the person logs in to another account, the old account's task in flight
// says nothing of it.
func TestSessionWindowOfTheCurrentAccountOnly(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	w.used = 20
	w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 100))
	if _, known, _ := w.gate.SessionWindow(ctx, "claude", 30*time.Minute); !known {
		t.Fatal("the current account's admission gave no window")
	}
	if err := w.store.Update(ctx, func(st *State) error {
		st.AccountKeys["claude/"+quota.IdentityAccount] = "account-b"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if used, known, _ := w.gate.SessionWindow(ctx, "claude", 30*time.Minute); known {
		t.Fatalf("another account's admission gave the window %g", used)
	}
}

// A refused admission's reading counts too: a newer, higher reading than the
// task in flight holds is the window.
func TestSessionWindowKeepsARefusedReading(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	w.used = 40
	w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 100))
	w.now = w.now.Add(20 * time.Minute)
	w.used = 99
	w.refused(w.request("claude", workerenv.Worker, workerenv.Free, 101), ReasonWindow)
	used, known, err := w.gate.SessionWindow(ctx, "claude", 30*time.Minute)
	if err != nil || !known || used != 99 {
		t.Fatalf("used %g, known %v: %v; want the refused reading's 99", used, known, err)
	}
}

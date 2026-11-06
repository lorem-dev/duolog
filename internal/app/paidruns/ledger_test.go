package paidruns

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

func usd(v float64) *float64 { return &v }

func mustSettle(t *testing.T, st *Store, in paidrules.Settlement) {
	t.Helper()
	if err := st.Settle(context.Background(), in); err != nil {
		t.Fatal(in.ID, err)
	}
}

func charged(t *testing.T, st *Store, id string) float64 {
	t.Helper()
	s, ok := readState(t, st).Settled[id]
	if !ok {
		t.Fatalf("%s not settled", id)
	}
	return s.ChargedUSD
}

func wantSpent(t *testing.T, st *Store, agent string, now time.Time, settled, inflight float64) {
	t.Helper()
	gotSettled, gotInflight := spent(t, st, agent, now)
	if math.Abs(gotSettled-settled) > 1e-9 || math.Abs(gotInflight-inflight) > 1e-9 {
		t.Fatalf("%s at %s: spent %v + %v in flight, want %v + %v", agent, now.Format(time.RFC3339),
			gotSettled, gotInflight, settled, inflight)
	}
}

func TestRollingLedgerMaxCostAndInflight(t *testing.T) {
	ctx := context.Background()

	t.Run("rolling 24 hours", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		ended := map[string]time.Time{
			"day-old":   now.Add(-24 * time.Hour),
			"edge":      now.Add(-24*time.Hour + time.Second),
			"before-12": now.Add(-20 * time.Minute),
		}
		priced := map[string]float64{"day-old": 1, "edge": 1.25, "before-12": 0.5}
		for _, id := range []string{"day-old", "edge", "before-12"} {
			mustAdmit(t, st, paidTask(id, "claude", 0.5))
			mustSettle(t, st, paidrules.Settlement{ID: id, PricedUSD: priced[id], Started: true, CompleteUsage: true, Ended: ended[id]})
		}
		// A settlement 24 hours old has left the day; one a second younger
		// has not.
		wantSpent(t, st, "claude", now, 1.75, 0)
		// Past midnight nothing resets: the settlement of 20 minutes before
		// midnight still counts, the edge one has aged out.
		wantSpent(t, st, "claude", now.Add(20*time.Minute), 0.5, 0)
		wantSpent(t, st, "claude", now.Add(23*time.Hour+39*time.Minute), 0.5, 0)
		wantSpent(t, st, "claude", now.Add(24*time.Hour-20*time.Minute), 0, 0)
		// Spend is per agent.
		wantSpent(t, st, "codex", now, 0, 0)
	})

	t.Run("larger of priced and reported", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		for _, tc := range []struct {
			id       string
			priced   float64
			reported *float64
			want     float64
		}{
			{"cli-higher", 0.5, usd(0.75), 0.75},
			{"tokens-higher", 1.25, usd(0.25), 1.25},
			{"no-cli-cost", 0.5, nil, 0.5},
			{"cli-zero", 0.25, usd(0), 0.25},
		} {
			mustAdmit(t, st, paidTask(tc.id, "claude", 0.5))
			// The caller's ChargedUSD is ignored: the Store sets spend.
			mustSettle(t, st, paidrules.Settlement{ID: tc.id, Agent: "claude", ChargedUSD: 100, PricedUSD: tc.priced,
				ReportedUSD: tc.reported, Input: 1000, Output: 100, Started: true, CompleteUsage: true})
			if got := charged(t, st, tc.id); got != tc.want {
				t.Fatalf("%s charged %v, want %v", tc.id, got, tc.want)
			}
		}
		wantSpent(t, st, "claude", now, 2.75, 0)
		if s := readState(t, st).Settled["cli-higher"]; !s.Ended.Equal(now) || *s.ReportedUSD != 0.75 || s.PricedUSD != 0.5 {
			t.Fatalf("settlement %+v", s)
		}
	})

	t.Run("in flight, replaced once", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		mustAdmit(t, st, paidTask("running", "claude", 0.5))
		wantSpent(t, st, "claude", now, 0, 0.5)
		// A free task holds no money.
		mustAdmit(t, st, freeTask("free", "claude", "personal", now))
		wantSpent(t, st, "claude", now, 0, 0.5)
		mustSettle(t, st, paidrules.Settlement{ID: "running", PricedUSD: 0.25, Started: true, CompleteUsage: true})
		wantSpent(t, st, "claude", now, 0.25, 0)
		// A duplicate settlement changes nothing, whatever it says.
		before := readState(t, st)
		mustSettle(t, st, paidrules.Settlement{ID: "running", PricedUSD: 9, ReportedUSD: usd(9), Started: true, CompleteUsage: true})
		mustSettle(t, st, paidrules.Settlement{ID: "running", Started: true})
		if after := readState(t, st); !reflect.DeepEqual(after, before) {
			t.Fatalf("duplicate settlement changed the state:\n%+v\n%+v", before, after)
		}
		wantSpent(t, st, "claude", now, 0.25, 0)
		// A reservation that never settled is in flight however old it is,
		// and the cap sees it.
		mustAdmit(t, st, paidTask("stuck", "claude", 4.5))
		now = now.Add(30 * time.Hour)
		wantSpent(t, st, "claude", now, 0, 4.5)
		mustRefuse(t, st, paidTask("after-stuck", "claude", 0.75), ReasonCap)
	})

	t.Run("zero and unavailable usage", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		mustAdmit(t, st, paidTask("zero", "claude", 0.5))
		mustSettle(t, st, paidrules.Settlement{ID: "zero", Started: true, CompleteUsage: true})
		mustAdmit(t, st, paidTask("unavailable", "claude", 0.5))
		mustSettle(t, st, paidrules.Settlement{ID: "unavailable", Started: true})
		// An interrupted attempt keeps at least its estimate, and more when
		// it measured more.
		mustAdmit(t, st, paidTask("interrupted", "claude", 0.5))
		mustSettle(t, st, paidrules.Settlement{ID: "interrupted", Input: 10_000, PricedUSD: 0.2, Started: true})
		mustAdmit(t, st, paidTask("interrupted-late", "claude", 0.5))
		mustSettle(t, st, paidrules.Settlement{ID: "interrupted-late", Input: 900_000, PricedUSD: 0.7, ReportedUSD: usd(0.6), Started: true})
		for id, want := range map[string]float64{"zero": 0, "unavailable": 0.5, "interrupted": 0.5, "interrupted-late": 0.7} {
			if got := charged(t, st, id); got != want {
				t.Fatalf("%s charged %v, want %v", id, got, want)
			}
		}
		wantSpent(t, st, "claude", now, 1.7, 0)
	})

	t.Run("unexpected spend on a free attempt", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		for _, id := range []string{"ordinary", "overage", "fast", "fast-cut"} {
			mustAdmit(t, st, freeTask(id, "codex", "personal", now))
		}
		// The CLI prices a subscription run too; that is no spend.
		mustSettle(t, st, paidrules.Settlement{ID: "ordinary", PricedUSD: 0.4, ReportedUSD: usd(0.3), Started: true, CompleteUsage: true})
		mustSettle(t, st, paidrules.Settlement{ID: "overage", PricedUSD: 0.4, ReportedUSD: usd(0.3), Started: true, CompleteUsage: true,
			Evidence: quota.RunEvidence{Expected: true, Seen: true, UsingOverage: true}})
		mustSettle(t, st, paidrules.Settlement{ID: "fast", PricedUSD: 0.2, ReportedUSD: usd(0.35), Started: true, CompleteUsage: true,
			Evidence: quota.RunEvidence{Fast: true}})
		mustSettle(t, st, paidrules.Settlement{ID: "fast-cut", PricedUSD: 0.1, Started: true, Evidence: quota.RunEvidence{Fast: true}})
		for id, want := range map[string]float64{"ordinary": 0, "overage": 0.4, "fast": 0.35, "fast-cut": 1} {
			if got := charged(t, st, id); got != want {
				t.Fatalf("%s charged %v, want %v", id, got, want)
			}
		}
		wantSpent(t, st, "codex", now, 1.75, 0)
	})

	t.Run("settled overrun", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		mustAdmit(t, st, paidTask("long", "claude", 1))
		cmd, stdin := waitingTask(t)
		if err := st.Attach(ctx, "long", paidrules.Owner{PID: cmd.Process.Pid, Birth: "test"}); err != nil {
			t.Fatal(err)
		}
		// The run went far past its estimate and the cap: the settlement
		// records it, does not fail and stops nothing.
		mustSettle(t, st, paidrules.Settlement{ID: "long", PricedUSD: 7, ReportedUSD: usd(6.5), Input: 5_000_000, Started: true, CompleteUsage: true})
		wantSpent(t, st, "claude", now, 7, 0)
		exitedNormally(t, cmd, stdin)
		// The next admission sees the overrun.
		mustRefuse(t, st, paidTask("next", "claude", 0.01), ReasonCap)
		now = now.Add(24 * time.Hour)
		mustAdmit(t, st, paidTask("next-day", "claude", 0.01))
	})

	t.Run("identity rotation keeps the cap", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		first, err := st.Identity("claude", quota.IdentityAccount, []byte("account-one"))
		if err != nil {
			t.Fatal(err)
		}
		d := mustAdmit(t, st, paidTask("before", "claude", 1))
		if d.Reservation.AccountKey != first {
			t.Fatalf("reservation account key %q, want the account kind's %q", d.Reservation.AccountKey, first)
		}
		mustSettle(t, st, paidrules.Settlement{ID: "before", PricedUSD: 4, Started: true, CompleteUsage: true})
		if s := readState(t, st).Settled["before"]; s.AccountKey != first || s.Agent != "claude" {
			t.Fatalf("settlement %+v", s)
		}
		if _, err := st.Identity("claude", quota.IdentityAccount, []byte("account-two")); err != nil {
			t.Fatal(err)
		}
		wantSpent(t, st, "claude", now, 4, 0)
		mustRefuse(t, st, paidTask("after", "claude", 1.5), ReasonCap)
	})

	t.Run("release only unstarted", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		mustAdmit(t, st, paidTask("never-started", "claude", 1))
		wantSpent(t, st, "claude", now, 0, 1)
		// A supervisor that died before the start leaves the hold in place:
		// nothing expires it.
		now = now.Add(48 * time.Hour)
		wantSpent(t, st, "claude", now, 0, 1)
		for range 2 {
			if err := st.Release(ctx, "never-started"); err != nil {
				t.Fatal(err)
			}
		}
		wantSpent(t, st, "claude", now, 0, 0)
		if s := readState(t, st); len(s.Reservations) != 0 || len(s.Settled) != 0 {
			t.Fatalf("release left %+v", s)
		}
		mustAdmit(t, st, paidTask("started", "claude", 1))
		if err := st.Attach(ctx, "started", paidrules.Owner{PID: 99, Birth: "b-99"}); err != nil {
			t.Fatal(err)
		}
		if err := st.Release(ctx, "started"); err == nil {
			t.Fatal("released a started task")
		}
		wantSpent(t, st, "claude", now, 0, 1)
		mustSettle(t, st, paidrules.Settlement{ID: "started", PricedUSD: 0.5, Started: true, CompleteUsage: true})
		if err := st.Release(ctx, "started"); err != nil {
			t.Fatal(err)
		}
		wantSpent(t, st, "claude", now, 0.5, 0)
		// A settlement for an attempt that never started still charges
		// measured spend, and never less than zero.
		mustAdmit(t, st, paidTask("refused-by-cli", "claude", 1))
		mustSettle(t, st, paidrules.Settlement{ID: "refused-by-cli", CompleteUsage: true})
		wantSpent(t, st, "claude", now, 0.5, 0)
	})

	t.Run("peer session outlives its task charge", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		open := paidTask("pane", "claude", 1)
		open.Reservation.Shape, open.Assessment.Shape = workerenv.Peer, workerenv.Peer
		mustAdmit(t, st, open)
		if err := st.Attach(ctx, "pane", paidrules.Owner{Pane: "pane-7", PeerSession: "peer-7"}); err != nil {
			t.Fatal(err)
		}
		mustSettle(t, st, paidrules.Settlement{ID: "pane", PricedUSD: 0.75, Started: true, CompleteUsage: true})
		s := readState(t, st)
		r, ok := s.Reservations["pane"]
		if !ok || !r.Session || r.Task || r.Estimate.USD != 0 || r.Child.Pane != "pane-7" {
			t.Fatalf("pane after its task settled: %+v", r)
		}
		if liveSessions(s, "claude") != 1 {
			t.Fatal("settling the task closed the pane's session")
		}
		wantSpent(t, st, "claude", now, 0.75, 0)
		// A worker's session ends with its task.
		mustAdmit(t, st, paidTask("worker", "claude", 1))
		mustSettle(t, st, paidrules.Settlement{ID: "worker", PricedUSD: 0.25, Started: true, CompleteUsage: true})
		if s := readState(t, st); liveSessions(s, "claude") != 1 {
			t.Fatalf("worker session kept after settlement: %+v", s.Reservations)
		}
	})

	t.Run("invalid settlements refuse", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		mustAdmit(t, st, paidTask("run", "claude", 1))
		before := readState(t, st)
		for name, in := range map[string]paidrules.Settlement{
			"unknown":        {ID: "missing", PricedUSD: 1},
			"no id":          {PricedUSD: 1},
			"other agent":    {ID: "run", Agent: "codex", PricedUSD: 1},
			"negative price": {ID: "run", PricedUSD: -1},
			"nan price":      {ID: "run", PricedUSD: math.NaN()},
			"inf reported":   {ID: "run", ReportedUSD: usd(math.Inf(1))},
			"negative cli":   {ID: "run", ReportedUSD: usd(-0.5)},
			"negative input": {ID: "run", Input: -1},
			"cached > input": {ID: "run", Input: 10, Cached: 11},
		} {
			if err := st.Settle(ctx, in); err == nil {
				t.Fatalf("%s settled", name)
			}
		}
		if after := readState(t, st); !reflect.DeepEqual(after, before) {
			t.Fatalf("invalid settlements changed the state:\n%+v\n%+v", before, after)
		}
	})
}

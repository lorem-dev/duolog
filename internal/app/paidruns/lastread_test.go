package paidruns

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// lastReadWorld is one account read by successive duolog commands, each with
// a cache of its own, as on a machine where a read can time out.
type lastReadWorld struct {
	t               *testing.T
	now             time.Time
	cfg             *config.Config
	store           *Store
	fail            error
	session, weekly float64
	account         string
	reads           int
}

func newLastReadWorld(t *testing.T) *lastReadWorld {
	w := &lastReadWorld{t: t, now: gateStart, cfg: gateConfig(), session: 30, weekly: 40, account: "account-a"}
	w.store = testStore(t, &w.now)
	return w
}

// read is a first-party subscription read that records the account, as the
// identity sink does, or the configured failure.
func (w *lastReadWorld) read(_ context.Context, p workerenv.Process) (quota.Snapshot, error) {
	w.reads++
	if w.fail != nil {
		return quota.Snapshot{}, w.fail
	}
	if _, err := w.store.Identity(p.Agent, quota.IdentityAccount, []byte(w.account)); err != nil {
		return quota.Snapshot{}, err
	}
	return quota.Snapshot{ReadAt: w.now, Plan: "max", Windows: []quota.Window{
		{Kind: "session", DurationMinutes: 300, UsedPercent: w.session, ResetsAt: w.now.Add(2 * time.Hour)},
		{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: w.weekly, ResetsAt: w.now.Add(72 * time.Hour)},
	}, Billing: quota.BillingFacts{AuthMethod: "claude.ai", APIProvider: "firstParty", RouteKnown: true, WindowsAvailable: yes()}}, nil
}

// cache is a new command's cache over the shared account state.
func (w *lastReadWorld) cache() *Cache {
	return &Cache{Read: w.read, Now: func() time.Time { return w.now }, State: w.store}
}

func (w *lastReadWorld) build(shape workerenv.Shape) func() (workerenv.Process, error) {
	return func() (workerenv.Process, error) {
		return workerenv.Build(workerenv.ProcessInput{Agent: "claude", Shape: shape, Variant: workerenv.Free,
			Config: w.cfg, Environ: []string{"HOME=/home/test"}, LeadID: "lead-1"})
	}
}

func (w *lastReadWorld) get(c *Cache, shape workerenv.Shape) (quota.Snapshot, Assessment, error) {
	w.t.Helper()
	_, s, a, err := c.Get(context.Background(), w.build(shape), w.cfg, quota.RunEvidence{}, false)
	return s, a, err
}

// good makes a successful read in a command of its own.
func (w *lastReadWorld) good(shape workerenv.Shape) {
	w.t.Helper()
	if _, a, err := w.get(w.cache(), shape); err != nil || a.Verdict != Subscription {
		w.t.Fatalf("good read: %v %+v", err, a)
	}
}

// TestLastGoodReadCoversATimedOutRead is the loaded machine: a read times out
// a few minutes after a good one of the same account and route, whose
// windows were far from their limits. The command uses that read, marked as
// cached, rather than a paid verdict.
func TestLastGoodReadCoversATimedOutRead(t *testing.T) {
	w := newLastReadWorld(t)
	start := w.now
	w.good(workerenv.Peer)
	w.now = w.now.Add(5 * time.Minute)
	w.fail = quota.ErrTimedOut
	c := w.cache()
	s, a, err := w.get(c, workerenv.Peer)
	if err != nil {
		t.Fatal(err)
	}
	if a.Verdict != Subscription || !s.Cached || !s.ReadAt.Equal(start) || len(s.Windows) != 2 {
		t.Fatalf("%+v %+v", a, s)
	}
	if latest, ok := c.Latest("claude"); !ok || !latest.Cached {
		t.Fatalf("duolog quota cannot show the cached read: %+v", latest)
	}
	// A lent read is never a cache hit: the next request reads again.
	reads := w.reads
	if _, _, err := w.get(c, workerenv.Peer); err != nil || w.reads != reads+1 {
		t.Fatalf("reads %d, want %d: %v", w.reads, reads+1, err)
	}
}

// TestLastGoodReadRefused: an older read, one nearer a limit, one of another
// account or route, or a read that failed other than by timing out, keeps
// today's answer: the failed read is paid.
func TestLastGoodReadRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(w *lastReadWorld)
		after func(w *lastReadWorld)
		shape workerenv.Shape
		fail  error
	}{
		{name: "older than ten minutes", after: func(w *lastReadWorld) { w.now = w.now.Add(10*time.Minute + time.Second) }},
		{name: "five-hour window at the margin", setup: func(w *lastReadWorld) { w.session = 80 }},
		{name: "weekly window past the margin", setup: func(w *lastReadWorld) { w.weekly = 85 }},
		{name: "another account since", after: func(w *lastReadWorld) {
			if _, err := w.store.Identity("claude", quota.IdentityAccount, []byte("account-b")); err != nil {
				w.t.Fatal(err)
			}
		}},
		{name: "another route", shape: workerenv.Worker},
		{name: "not a timeout", fail: errors.New("not logged in")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newLastReadWorld(t)
			if tc.setup != nil {
				tc.setup(w)
			}
			w.good(workerenv.Peer)
			w.now = w.now.Add(time.Minute)
			if tc.after != nil {
				tc.after(w)
			}
			w.fail = quota.ErrTimedOut
			if tc.fail != nil {
				w.fail = tc.fail
			}
			shape := workerenv.Peer
			if tc.shape != "" {
				shape = tc.shape
			}
			s, a, err := w.get(w.cache(), shape)
			if err == nil || s.Cached || a.Verdict != Paid ||
				!slices.Contains(a.Signals, workerenv.Signal{Name: "read:failed", Layer: "/usage"}) {
				t.Fatalf("%v %+v %+v", err, a, s)
			}
		})
	}
}

// TestCachedReadVerdict: a verdict trusts a lent read only while it is young
// and clear of every limit, whatever balance.max_age_seconds says.
func TestCachedReadVerdict(t *testing.T) {
	w := newLastReadWorld(t)
	p, err := w.build(workerenv.Peer)()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := w.read(context.Background(), p)
	s.Cached = true
	for _, tc := range []struct {
		name string
		age  time.Duration
		used float64
		want Verdict
	}{
		{"young and clear", 9 * time.Minute, 79, Subscription},
		{"too old", 11 * time.Minute, 10, Paid},
		{"near a limit", time.Minute, 80, Paid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := s
			c.Windows = slices.Clone(s.Windows)
			c.Windows[0].UsedPercent = tc.used
			a := Evaluate(p, c, nil, quota.RunEvidence{}, w.cfg, s.ReadAt.Add(tc.age))
			if a.Verdict != tc.want {
				t.Fatalf("%+v", a)
			}
			if tc.want == Paid && !slices.Contains(a.Signals, workerenv.Signal{Name: "read:stale", Layer: "/usage"}) {
				t.Fatalf("%+v", a.Signals)
			}
		})
	}
}

// timingOutGate is a gate world whose reads record account A, and time out
// once *fail is set. reads counts them.
func timingOutGate(t *testing.T, fail *bool, reads *int) *gateWorld {
	w := newGateWorld(t)
	w.gate.Cache = &Cache{State: w.store, Now: func() time.Time { return w.now },
		Read: func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
			*reads++
			if *fail {
				return quota.Snapshot{}, quota.ErrTimedOut
			}
			if _, err := w.store.Identity(p.Agent, quota.IdentityAccount, []byte("account-a")); err != nil {
				return quota.Snapshot{}, err
			}
			return w.read(ctx, p)
		}}
	return w
}

// TestLentReadTeachesNoRates: a run admitted on a lent read learns no window
// rates, since the read before it is minutes older than the run.
func TestLentReadTeachesNoRates(t *testing.T) {
	fail, reads := false, 0
	w := timingOutGate(t, &fail, &reads)
	w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 100))
	w.now = w.now.Add(3 * time.Minute)
	fail = true
	p := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 101))
	if !p.Snapshot.Cached {
		t.Fatalf("%+v", p.Snapshot)
	}
	before := reads
	w.finish(p, Settlement{ID: p.Reservation.ID, Started: true, CompleteUsage: true, Input: 1000, Output: 100, Ended: w.now})
	if reads != before {
		t.Fatalf("the run's end read the quota %d times to learn from a lent read", reads-before)
	}
}

// TestPeerOpenSurvivesATimedOutRead is the reported refusal: a peer open
// whose read times out is admitted on the last good read of the account.
func TestPeerOpenSurvivesATimedOutRead(t *testing.T) {
	fail, reads := false, 0
	w := timingOutGate(t, &fail, &reads)
	first := w.acquire(w.peerOpenRequest("claude", workerenv.Free, 101))
	if err := w.gate.CloseSession(context.Background(), first.Reservation.ID); err != nil {
		t.Fatal(err)
	}
	w.now = w.now.Add(3 * time.Minute)
	fail = true
	p := w.acquire(w.peerOpenRequest("claude", workerenv.Free, 102))
	if p.Assessment.Verdict != Subscription || !p.Snapshot.Cached {
		t.Fatalf("%+v %+v", p.Assessment, p.Snapshot)
	}
}

package paidruns

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// gateStart is the fake clock's start.
var gateStart = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)

// gateSecret is a key in duolog's environment; no refusal may show it.
const gateSecret = "sk-ant-gate-secret"

// gateWorld is a gate over a temporary account state, a fake clock that
// only waits move, fake quota reads and fake process and pane probes.
type gateWorld struct {
	t      *testing.T
	now    time.Time
	cfg    *config.Config
	store  *Store
	gate   *Gate
	reads  int
	builds int
	// used is the five-hour window's used percent in the next read.
	used float64
	// paidRoute makes the reads of an agent show an API key route.
	paidRoute map[string]bool
	overage   bool
	procs     map[int]Presence
	panes     map[string]Presence
	waits     []time.Duration
	// onWait runs after the n-th wait moved the clock.
	onWait func(n int)
}

// gateConfig prices the test families and sets the default session limits.
func gateConfig() *config.Config {
	cfg := pricedConfig()
	cfg.Limits.SessionsByTier = config.DefaultSessionTiers()
	cfg.Limits.MaxTotalSessions = 10
	cfg.Limits.SessionWaitSeconds = 60
	cfg.Balance = config.DefaultBalance()
	return cfg
}

func newGateWorld(t *testing.T) *gateWorld {
	t.Helper()
	w := &gateWorld{t: t, now: gateStart, cfg: gateConfig(), used: 10,
		paidRoute: map[string]bool{}, procs: map[int]Presence{}, panes: map[string]Presence{}}
	w.store = testStore(t, &w.now)
	clock := func() time.Time { return w.now }
	w.gate = &Gate{
		Store:  w.store,
		Cache:  &Cache{Read: w.read, Now: clock},
		Config: w.cfg,
		Reconcile: ReconcileInput{
			Process: func(o Owner) Presence { return w.presence(w.procs[o.PID]) },
			Pane:    func(_ context.Context, o Owner) Presence { return w.presence(w.panes[o.Pane]) },
		},
		Now:  clock,
		Wait: w.wait,
		Layers: func(context.Context, workerenv.ProcessInput) ([]workerenv.LayerFacts, error) {
			w.builds++
			return nil, nil
		},
	}
	return w
}

// presence is Alive for an owner the test never named.
func (w *gateWorld) presence(p Presence) Presence {
	if p == Unknown {
		return Alive
	}
	return p
}

func (w *gateWorld) wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.waits = append(w.waits, d)
	w.now = w.now.Add(d)
	if w.onWait != nil {
		w.onWait(len(w.waits))
	}
	return ctx.Err()
}

func (w *gateWorld) waited() time.Duration {
	var total time.Duration
	for _, d := range w.waits {
		total += d
	}
	return total
}

// read is a first-party subscription read at the current time, or an API
// key route for a paid variant or an agent marked paidRoute.
func (w *gateWorld) read(_ context.Context, p workerenv.Process) (quota.Snapshot, error) {
	w.reads++
	s := quota.Snapshot{ReadAt: w.now, Windows: []quota.Window{
		{Kind: "session", DurationMinutes: 300, UsedPercent: w.used, ResetsAt: w.now.Add(2 * time.Hour)},
		{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 20, ResetsAt: w.now.Add(72 * time.Hour)},
	}}
	switch p.Agent {
	case "claude":
		s.Billing = quota.BillingFacts{AuthMethod: "claude.ai", APIProvider: "firstParty", RouteKnown: true, WindowsAvailable: yes()}
	case "codex":
		s.Billing = quota.BillingFacts{AccountType: "chatgpt", AuthMode: "chatgpt", RouteKnown: true, OrdinaryUsageAllowed: yes()}
	}
	if p.Variant == workerenv.Paid || w.paidRoute[p.Agent] {
		s.Billing.RouteSignals = []workerenv.Signal{{Name: "auth:apiKey", Layer: "auth status"}}
	}
	s.Billing.OverageEnabled = w.overage
	return s, nil
}

// request is a delegation of agent's test family by supervisor pid.
func (w *gateWorld) request(agent string, shape workerenv.Shape, variant workerenv.Variant, pid int) Request {
	family := "sonnet"
	if agent == "codex" {
		family = "sol"
	}
	return Request{
		Input: workerenv.ProcessInput{Agent: agent, Shape: shape, Variant: variant, Config: w.cfg,
			Environ: []string{"HOME=/home/test", "ANTHROPIC_API_KEY=" + gateSecret}, LeadID: "lead-1", Role: "implement"},
		Family: family, Role: "implement",
		Owner: Owner{PID: pid, Birth: fmt.Sprintf("b-%d", pid)},
	}
}

// peerOpenRequest opens a pane of agent.
func (w *gateWorld) peerOpenRequest(agent string, variant workerenv.Variant, pid int) Request {
	req := w.request(agent, workerenv.Peer, variant, pid)
	req.OpenPeer = true
	return req
}

// peerSend sends a task to the open session.
func (w *gateWorld) peerSend(agent string, variant workerenv.Variant, pid int, session string) Request {
	req := w.request(agent, workerenv.Peer, variant, pid)
	req.ExistingSession = session
	return req
}

func (w *gateWorld) acquire(req Request) Permit {
	w.t.Helper()
	p, err := w.gate.Acquire(context.Background(), req)
	if err != nil {
		w.t.Fatalf("%s %s %s refused: %v", req.Input.Agent, req.Input.Shape, req.Input.Variant, err)
	}
	if p.Reservation.ID == "" || p.Process.Agent != req.Input.Agent {
		w.t.Fatalf("permit without a reservation or of another agent: %+v", p)
	}
	return p
}

// refused checks that req is refused with reason and that no reservation of
// its supervisor was written.
func (w *gateWorld) refused(req Request, reason string) *Refusal {
	w.t.Helper()
	before := len(w.state().Reservations)
	p, err := w.gate.Acquire(context.Background(), req)
	var r *Refusal
	if !errors.As(err, &r) {
		w.t.Fatalf("got %+v, %v; want a refusal with %q", p.Reservation, err, reason)
	}
	if r.Reason != reason {
		w.t.Fatalf("refused with %q (%v), want %q", r.Reason, r, reason)
	}
	if strings.Contains(r.Error(), gateSecret) {
		w.t.Fatalf("refusal shows a value: %v", r)
	}
	st := w.state()
	if len(st.Reservations) > before {
		w.t.Fatalf("a refusal wrote a reservation: %+v", st.Reservations)
	}
	for _, r := range st.Reservations {
		if r.Supervisor == req.Owner {
			w.t.Fatalf("a refusal holds %+v", r)
		}
	}
	return r
}

func (w *gateWorld) finish(p Permit, s Settlement) {
	w.t.Helper()
	if err := w.gate.Finish(context.Background(), p, s); err != nil {
		w.t.Fatal(err)
	}
}

func (w *gateWorld) state() State {
	w.t.Helper()
	return readState(w.t, w.store)
}

// live counts the held sessions and tasks of agent, "" for every agent.
func (w *gateWorld) live(agent string) (sessions, tasks int) {
	w.t.Helper()
	for _, r := range w.state().Reservations {
		if agent != "" && r.Agent != agent {
			continue
		}
		if r.Session {
			sessions++
		}
		if r.Task {
			tasks++
		}
	}
	return sessions, tasks
}

func (w *gateWorld) wantLive(agent string, sessions, tasks int) {
	w.t.Helper()
	if s, k := w.live(agent); s != sessions || k != tasks {
		w.t.Fatalf("%s: %d sessions and %d tasks held, want %d and %d: %+v", agent, s, k, sessions, tasks, w.state().Reservations)
	}
}

// A live session is a running worker process, fresh, resumed or a
// calibration cell, or an open peer pane, idle or busy. A dormant thread and
// the lead hold nothing. A peer send runs a task in its pane's session and
// takes no slot; the first send takes over the task the open held. A paid or
// workspace agent may keep three panes but runs one task at a time. Session
// and task are one reservation of one transaction, and reconciliation runs
// before each grant.
func TestGateCountsProcessesAndPanes(t *testing.T) {
	t.Run("workers and cells", func(t *testing.T) {
		w := newGateWorld(t)
		fresh := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 101))
		resumed := w.request("claude", workerenv.Worker, workerenv.Free, 102)
		resumed.Role = "debug"
		w.acquire(resumed)
		cell := w.request("claude", workerenv.Worker, workerenv.Free, 103)
		cell.Calibration = true
		w.acquire(cell)
		w.wantLive("claude", 3, 3)
		for _, r := range w.state().Reservations {
			if !r.Session || !r.Task || r.Shape != workerenv.Worker || r.Started {
				t.Fatalf("a worker holds a session and a task in one reservation: %+v", r)
			}
		}
		// A personal subscription without measured rates is tier small: 3.
		r := w.refused(w.request("claude", workerenv.Worker, workerenv.Free, 104), ReasonAgentSessions)
		if r.Live != 3 || r.Limit != 3 || r.Tier != TierSmall || !r.Slot() {
			t.Fatalf("refusal: %+v", r)
		}
		for _, want := range []string{"claude worker", "sessions 3 of 3 (tier small)", "3 of 10 in all", "estimate"} {
			if !strings.Contains(r.Error(), want) {
				t.Errorf("refusal %q lacks %q", r.Error(), want)
			}
		}
		// The ended run's thread stays resumable, but holds no slot.
		w.finish(fresh, Settlement{Started: true, CompleteUsage: true})
		w.wantLive("claude", 2, 2)
		w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 105))
		w.wantLive("claude", 3, 3)
		// Another agent has its own slots; the total counts both.
		w.acquire(w.request("codex", workerenv.Worker, workerenv.Free, 106))
		w.wantLive("", 4, 4)
	})

	t.Run("peer panes and sends", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.PaidRuns.Claude.Allow, w.cfg.PaidRuns.Claude.DailyCapUSD = true, 50
		w.cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
		open := w.acquire(w.peerOpenRequest("claude", workerenv.Paid, 201))
		session := open.Reservation.ID
		if r := w.state().Reservations[session]; !r.Session || !r.Task || r.Estimate.USD <= 0 {
			t.Fatalf("an open pane holds its slot and one task in one reservation: %+v", r)
		}
		w.wantLive("claude", 1, 1)
		_, inflight := spent(t, w.store, "claude", w.now)

		// The first send takes over the open's task: no new slot, no new spend.
		first := w.acquire(w.peerSend("claude", workerenv.Paid, 202, session))
		if first.Reservation.ID == session || first.Reservation.SessionID != session ||
			first.Reservation.Session || !first.Reservation.Task {
			t.Fatalf("first send: %+v", first.Reservation)
		}
		w.wantLive("claude", 1, 1)
		if _, after := spent(t, w.store, "claude", w.now); !near(after, inflight) {
			t.Fatalf("the first send reserved again: in flight %v, then %v", inflight, after)
		}
		if r := w.state().Reservations[session]; !r.Session || r.Task {
			t.Fatalf("the open still holds its task after the transfer: %+v", r)
		}
		// A paid agent runs one task at a time; a send never waits for a
		// slot it does not need, but this one may not wait at all.
		w.refused(w.peerSend("claude", workerenv.Paid, 203, session), ReasonOneTask)
		w.finish(first, Settlement{Started: true, CompleteUsage: true, PricedUSD: 1})
		// The idle pane still counts.
		w.wantLive("claude", 1, 0)

		// The second send reserves a new task in the same session.
		second := w.acquire(w.peerSend("claude", workerenv.Paid, 204, session))
		if second.Reservation.Session || second.Reservation.SessionID != session {
			t.Fatalf("second send: %+v", second.Reservation)
		}
		w.wantLive("claude", 1, 1)
		if _, after := spent(t, w.store, "claude", w.now); !near(after, second.Reservation.Estimate.USD) {
			t.Fatalf("second send in flight %v, want its estimate", after)
		}
		w.finish(second, Settlement{Started: true, CompleteUsage: true, PricedUSD: 1})

		// A send to a session that is not open is refused.
		w.refused(w.peerSend("claude", workerenv.Paid, 205, "no-such-session"), ReasonNoSession)
		// Closing the pane frees its slot; its sends can no longer run.
		if err := w.gate.CloseSession(context.Background(), session); err != nil {
			t.Fatal(err)
		}
		w.wantLive("claude", 0, 0)
		w.refused(w.peerSend("claude", workerenv.Paid, 206, session), ReasonNoSession)

		// Closing a pane that never ran a task refunds the open's task.
		unused := w.acquire(w.peerOpenRequest("claude", workerenv.Paid, 207))
		if err := w.gate.CloseSession(context.Background(), unused.Reservation.ID); err != nil {
			t.Fatal(err)
		}
		if _, after := spent(t, w.store, "claude", w.now); after != 0 {
			t.Fatalf("an unused open kept %v USD in flight", after)
		}
		if _, ok := w.state().Settled[unused.Reservation.ID]; ok {
			t.Fatal("an unused open was charged")
		}
	})

	t.Run("workspace keeps three panes but runs one task", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.PaidRuns.Claude.Account = "workspace"
		var sessions []string
		for i := range 3 {
			open := w.acquire(w.peerOpenRequest("claude", workerenv.Free, 300+i))
			if i < 2 {
				// Another open waits until this pane's task is taken and done.
				w.refused(w.peerOpenRequest("claude", workerenv.Free, 390), ReasonOneTask)
			}
			send := w.acquire(w.peerSend("claude", workerenv.Free, 310+i, open.Reservation.ID))
			w.finish(send, Settlement{Started: true, CompleteUsage: true})
			sessions = append(sessions, open.Reservation.ID)
		}
		w.wantLive("claude", 3, 0)
		busy := w.acquire(w.peerSend("claude", workerenv.Free, 320, sessions[0]))
		w.refused(w.peerSend("claude", workerenv.Free, 321, sessions[1]), ReasonOneTask)
		w.refused(w.request("claude", workerenv.Worker, workerenv.Free, 322), ReasonOneTask)
		w.finish(busy, Settlement{Started: true, CompleteUsage: true})
		// A workspace account is tier small: a fourth pane has no slot.
		r := w.refused(w.peerOpenRequest("claude", workerenv.Free, 323), ReasonAgentSessions)
		if r.Tier != TierSmall || r.Limit != 3 {
			t.Fatalf("workspace refusal: %+v", r)
		}
		if !strings.Contains(r.Error(), "close") {
			t.Errorf("a refused pane names the close command: %q", r.Error())
		}
	})

	t.Run("reconciliation before each grant", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.Limits.SessionsByTier.Small = 2
		crashed := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 401))
		launched := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 402))
		if err := w.store.Launch(context.Background(), launched.Reservation.ID); err != nil {
			t.Fatal(err)
		}
		// Both supervisors die. The one that never began a launch is proven
		// unstarted and frees its slot; the other may have a child.
		w.procs[401], w.procs[402] = Gone, Gone
		w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 403))
		st := w.state()
		if _, ok := st.Reservations[crashed.Reservation.ID]; ok {
			t.Fatal("the gate granted without reconciling the dead supervisor first")
		}
		if r, ok := st.Reservations[launched.Reservation.ID]; !ok || r.Retained == "" {
			t.Fatalf("a launching reservation of a dead supervisor was freed: %+v", r)
		}
		w.refused(w.request("claude", workerenv.Worker, workerenv.Free, 404), ReasonAgentSessions)
	})
}

// A settled run that used overage keeps the next admission at
// subscription_overage while the read is older than the run; a fresh read
// clears it. A failed read is paid and refuses the free variant.
func TestGateKeepsRunEvidenceUntilFreshRead(t *testing.T) {
	w := newGateWorld(t)
	first := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 501))
	w.now = w.now.Add(10 * time.Second)
	w.finish(first, Settlement{Started: true, CompleteUsage: true, PricedUSD: 0.4,
		Evidence: quota.RunEvidence{Expected: true, Seen: true, UsingOverage: true}})
	if got := w.state().Settled[first.Reservation.ID].ChargedUSD; !near(got, 0.4) {
		t.Fatalf("a run on overage charged %v", got)
	}
	w.now = w.now.Add(10 * time.Second)
	cached := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 502))
	if cached.Assessment.Verdict != SubscriptionOverage {
		t.Fatalf("after an overage run on a cached read: %s", cached.Assessment.Verdict)
	}
	w.finish(cached, Settlement{Started: true, CompleteUsage: true})
	w.now = w.now.Add(5 * time.Minute)
	fresh := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 503))
	if fresh.Assessment.Verdict != Subscription {
		t.Fatalf("after a fresh read: %s", fresh.Assessment.Verdict)
	}

	failing := newGateWorld(t)
	failing.gate.Cache.Read = func(context.Context, workerenv.Process) (quota.Snapshot, error) {
		return quota.Snapshot{}, errors.New("usage read failed")
	}
	r := failing.refused(failing.request("claude", workerenv.Worker, workerenv.Free, 504), ReasonFreeVariantPaid)
	if r.Verdict != Paid || !strings.Contains(r.Error(), "read:failed") {
		t.Fatalf("failed read refusal: %+v", r)
	}
}

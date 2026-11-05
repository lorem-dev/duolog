package paidruns

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const (
	queueHelperMode = "DUOLOG_TEST_QUEUE_HELPER"
	queueHelperDir  = "DUOLOG_TEST_QUEUE_DIR"
)

// queueConfig has one claude slot, so one holder makes everyone wait.
func queueConfig() *config.Config {
	cfg := gateConfig()
	cfg.Limits.SessionsByTier.Small = 1
	return cfg
}

// queueGate is a gate on real time for helper processes: subscription
// reads, every owner alive, and short real waits.
func queueGate(st *Store, cfg *config.Config) *Gate {
	read := func(_ context.Context, p workerenv.Process) (quota.Snapshot, error) {
		now := time.Now()
		return quota.Snapshot{ReadAt: now, Windows: []quota.Window{
			{Kind: "session", DurationMinutes: 300, UsedPercent: 10, ResetsAt: now.Add(2 * time.Hour)},
		}, Billing: quota.BillingFacts{AuthMethod: "claude.ai", APIProvider: "firstParty", RouteKnown: true, WindowsAvailable: yes()}}, nil
	}
	return &Gate{Store: st, Cache: &Cache{Read: read}, Config: cfg,
		Reconcile: ReconcileInput{Process: func(Owner) Presence { return Alive }},
		Wait: func(ctx context.Context, _ time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
				return nil
			}
		}}
}

func queueRequest(cfg *config.Config, owner Owner, wait time.Duration) Request {
	return Request{Input: workerenv.ProcessInput{Agent: "claude", Shape: workerenv.Worker, Variant: workerenv.Free,
		Config: cfg, LeadID: "lead-1", Role: "implement"},
		Family: "sonnet", Role: "implement", Owner: owner, Wait: wait}
}

// TestQueueHelperProcess is not a test: the queue test starts the test
// binary with it as a waiting delegation of another process. It waits for a
// claude slot without a deadline and prints the reservation it was granted.
func TestQueueHelperProcess(t *testing.T) {
	name := os.Getenv(queueHelperMode)
	if name == "" {
		t.Skip("helper process only")
	}
	st, err := OpenUserState(os.Getenv(queueHelperDir))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg := queueConfig()
	p, err := queueGate(st, cfg).Acquire(ctx, queueRequest(cfg, Owner{PID: os.Getpid(), Birth: name}, -1))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println("granted " + p.Reservation.ID)
	os.Exit(0)
}

type waiter struct {
	name  string
	lines chan string
}

func startWaiter(t *testing.T, dir, name string) *waiter {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestQueueHelperProcess$")
	cmd.Env = append(os.Environ(), queueHelperMode+"="+name, queueHelperDir+"="+dir)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	w := &waiter{name: name, lines: make(chan string, 4)}
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			w.lines <- scanner.Text()
		}
		close(w.lines)
	}()
	return w
}

func (w *waiter) granted(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-w.lines:
		id, found := strings.CutPrefix(line, "granted ")
		if !ok || !found {
			t.Fatalf("%s: %q", w.name, line)
		}
		return id
	case <-time.After(30 * time.Second):
		t.Fatalf("%s was never granted a slot", w.name)
	}
	return ""
}

func (w *waiter) stillWaiting(t *testing.T) {
	t.Helper()
	select {
	case line := <-w.lines:
		t.Fatalf("%s jumped the queue: %q", w.name, line)
	default:
	}
}

// ticketOf waits until the owner named birth has a ticket and returns it.
func ticketOf(t *testing.T, st *Store, birth string) Ticket {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		for _, tk := range readState(t, st).Tickets {
			if tk.Owner.Birth == birth {
				return tk
			}
		}
	}
	t.Fatalf("%s never took a ticket", birth)
	return Ticket{}
}

// addTicket puts an earlier waiter of agent in the queue.
func addTicket(t *testing.T, st *Store, agent string, owner Owner, expires time.Time) {
	t.Helper()
	err := st.Update(context.Background(), func(s *State) error {
		s.NextTicket++
		s.Tickets = append(s.Tickets, Ticket{ID: fmt.Sprintf("early-%d", s.NextTicket), Agent: agent,
			Sequence: s.NextTicket, Owner: owner, Expires: expires})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Waiters take durable tickets in arrival order across processes. A freed
// slot goes to the oldest waiter that can use it; a new arrival cannot jump
// it. The grant rebuilds the process and reads quota afresh, and a request
// that lost its headroom, cap or opt-in while waiting is refused, never
// launched. Expired, canceled and dead waiters leave the queue. A delegation
// waits session_wait_seconds, 60 s by default, and then the next candidate
// is tried; a pane open refuses at once; a calibration cell waits until a
// slot frees or it is canceled.
func TestSlotQueueFirstComeAndFreshRead(t *testing.T) {
	ctx := context.Background()

	t.Run("helper processes wait in ticket order", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "accounts")
		st, err := OpenUserState(dir)
		if err != nil {
			t.Fatal(err)
		}
		cfg := queueConfig()
		gate := queueGate(st, cfg)
		holder, err := gate.Acquire(ctx, queueRequest(cfg, Owner{PID: 1, Birth: "holder"}, 0))
		if err != nil {
			t.Fatal(err)
		}
		first := startWaiter(t, dir, "helper-1")
		t1 := ticketOf(t, st, "helper-1")
		second := startWaiter(t, dir, "helper-2")
		t2 := ticketOf(t, st, "helper-2")
		if t1.Sequence >= t2.Sequence {
			t.Fatalf("tickets out of arrival order: %+v %+v", t1, t2)
		}
		// While the slot is full, a delegation that may not wait is refused.
		_, err = gate.Acquire(ctx, queueRequest(cfg, Owner{PID: 2, Birth: "newcomer"}, 0))
		var r *Refusal
		if !errors.As(err, &r) || !r.Slot() {
			t.Fatalf("newcomer: %v", err)
		}
		if err := st.Release(ctx, holder.Reservation.ID); err != nil {
			t.Fatal(err)
		}
		id1 := first.granted(t)
		s := readState(t, st)
		if got := s.Reservations[id1].Supervisor.Birth; got != "helper-1" {
			t.Fatalf("the slot went to %q, not the oldest waiter", got)
		}
		second.stillWaiting(t)
		if len(s.Tickets) != 1 || s.Tickets[0].Owner.Birth != "helper-2" {
			t.Fatalf("tickets after the first grant: %+v", s.Tickets)
		}
		if err := st.Release(ctx, id1); err != nil {
			t.Fatal(err)
		}
		id2 := second.granted(t)
		s = readState(t, st)
		if s.Reservations[id2].Supervisor.Birth != "helper-2" || len(s.Tickets) != 0 {
			t.Fatalf("after the second grant: %+v %+v", s.Reservations, s.Tickets)
		}
	})

	t.Run("a new arrival cannot jump a live waiter", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.Limits.SessionsByTier.Small = 1
		holder := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 601))
		addTicket(t, w.store, "claude", Owner{PID: 602, Birth: "b-602"}, time.Time{})
		w.finish(holder, Settlement{})
		w.wantLive("claude", 0, 0)
		r := w.refused(w.request("claude", workerenv.Worker, workerenv.Free, 603), ReasonQueue)
		if !r.Slot() || len(w.waits) != 0 {
			t.Fatalf("queued refusal: %+v, waits %v", r, w.waits)
		}
		// The waiter's process is proven gone: its ticket leaves.
		w.procs[602] = Gone
		w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 603))
		if tickets := w.state().Tickets; len(tickets) != 0 {
			t.Fatalf("a dead waiter's ticket stayed: %+v", tickets)
		}

		// Total contention is ordered across agents: an earlier codex waiter
		// with room of its own has the last total slot.
		w = newGateWorld(t)
		w.cfg.Limits.MaxTotalSessions = 1
		holder = w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 611))
		addTicket(t, w.store, "codex", Owner{PID: 612, Birth: "b-612"}, time.Time{})
		w.finish(holder, Settlement{})
		w.refused(w.request("claude", workerenv.Worker, workerenv.Free, 613), ReasonQueue)
		// With room for both, an earlier waiter of another agent blocks nothing.
		w.cfg.Limits.MaxTotalSessions = 10
		w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 613))
	})

	t.Run("an expired ticket is removed", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.Limits.SessionsByTier.Small = 1
		addTicket(t, w.store, "claude", Owner{PID: 621, Birth: "b-621"}, w.now.Add(-time.Second))
		w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 622))
		if tickets := w.state().Tickets; len(tickets) != 0 {
			t.Fatalf("an expired ticket stayed: %+v", tickets)
		}
	})

	t.Run("a delegation waits 60 s, then the next candidate", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.Limits.SessionsByTier.Small = 1
		w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 701))
		req := w.request("claude", workerenv.Worker, workerenv.Free, 702)
		req.Wait = SessionWait(w.cfg)
		if req.Wait != 60*time.Second {
			t.Fatalf("default wait %v", req.Wait)
		}
		w.onWait = func(int) {
			tickets := w.state().Tickets
			if len(tickets) != 1 || tickets[0].Owner != req.Owner || !tickets[0].Expires.Equal(gateStart.Add(time.Minute)) {
				t.Errorf("waiting without its durable ticket: %+v", tickets)
			}
		}
		r := w.refused(req, ReasonAgentSessions)
		if w.waited() != time.Minute || r.Waited != time.Minute || !w.now.Equal(gateStart.Add(time.Minute)) {
			t.Fatalf("waited %v (refusal says %v), want exactly 60 s", w.waited(), r.Waited)
		}
		if !strings.Contains(r.Error(), "sessions 1 of 1 (tier small); waited 60 s") {
			t.Errorf("lead line material: %q", r.Error())
		}
		// No slot freed, so no fresh read was made while waiting.
		if w.reads != 1 {
			t.Errorf("%d reads while the slot stayed full", w.reads)
		}
		if tickets := w.state().Tickets; len(tickets) != 0 {
			t.Fatalf("a refused waiter left its ticket: %+v", tickets)
		}
		w.onWait = nil
		w.acquire(w.request("codex", workerenv.Worker, workerenv.Free, 703))
	})

	t.Run("a grant rebuilds and reads afresh", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.Limits.SessionsByTier.Small = 1
		holder := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 801))
		req := w.request("claude", workerenv.Worker, workerenv.Free, 802)
		req.Wait = time.Minute
		w.onWait = func(n int) {
			if n == 3 {
				w.finish(holder, Settlement{})
			}
		}
		p := w.acquire(req)
		if w.waited() != 3*time.Second || w.reads != 2 || w.builds != 3 {
			t.Fatalf("waited %v, %d reads, %d builds; want 3 s, a fresh read and a rebuild on the grant", w.waited(), w.reads, w.builds)
		}
		if !p.Reservation.Created.Equal(w.now) || len(w.state().Tickets) != 0 {
			t.Fatalf("grant: %+v, tickets %+v", p.Reservation, w.state().Tickets)
		}
	})

	t.Run("headroom lost while waiting refuses", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.Limits.SessionsByTier.Small = 1
		holder := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 811))
		req := w.request("claude", workerenv.Worker, workerenv.Free, 812)
		req.Wait = time.Minute
		w.onWait = func(n int) {
			if n == 3 {
				w.used = 95
				w.finish(holder, Settlement{})
			}
		}
		w.refused(req, ReasonWindow)
		if w.reads != 2 || len(w.state().Tickets) != 0 {
			t.Fatalf("%d reads, tickets %+v", w.reads, w.state().Tickets)
		}
	})

	t.Run("cap lost while waiting refuses", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.PaidRuns.Claude.Allow, w.cfg.PaidRuns.Claude.DailyCapUSD = true, 6
		w.cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
		holder := w.acquire(w.request("claude", workerenv.Worker, workerenv.Paid, 821))
		req := w.request("claude", workerenv.Worker, workerenv.Paid, 822)
		req.Wait = time.Minute
		w.onWait = func(n int) {
			if n == 2 {
				w.finish(holder, Settlement{Started: true, CompleteUsage: true, PricedUSD: 4})
			}
		}
		r := w.refused(req, ReasonCap)
		if r.EstimateUSD <= 0 || r.EstimateSource == "" || !strings.Contains(r.Error(), "USD") {
			t.Fatalf("cap refusal without its estimate: %+v", r)
		}
	})

	t.Run("opt-in lost while waiting refuses", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.PaidRuns.Claude.Allow, w.cfg.PaidRuns.Claude.DailyCapUSD = true, 50
		w.cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
		holder := w.acquire(w.request("claude", workerenv.Worker, workerenv.Paid, 831))
		req := w.request("claude", workerenv.Worker, workerenv.Paid, 832)
		req.Wait = time.Minute
		w.onWait = func(n int) {
			if n == 2 {
				w.cfg.PaidRuns.Claude.Allow = false
				w.finish(holder, Settlement{})
			}
		}
		w.refused(req, ReasonNotAllowed)
		if len(w.state().Tickets) != 0 {
			t.Fatalf("tickets %+v", w.state().Tickets)
		}
	})

	t.Run("a pane open refuses at once", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.Limits.SessionsByTier.Small = 1
		w.acquire(w.peerOpenRequest("claude", workerenv.Free, 901))
		req := w.peerOpenRequest("claude", workerenv.Free, 902)
		req.Wait = time.Minute
		r := w.refused(req, ReasonAgentSessions)
		if len(w.waits) != 0 || r.Waited != 0 || len(w.state().Tickets) != 0 {
			t.Fatalf("a pane open waited: %v %+v", w.waits, w.state().Tickets)
		}
	})

	t.Run("calibration waits past 60 s until a slot or cancellation", func(t *testing.T) {
		w := newGateWorld(t)
		w.cfg.Limits.SessionsByTier.Small = 1
		holder := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 911))
		cell := w.request("claude", workerenv.Worker, workerenv.Free, 912)
		cell.Calibration = true
		w.onWait = func(n int) {
			if n == 90 {
				w.finish(holder, Settlement{})
			}
		}
		p := w.acquire(cell)
		if w.waited() != 90*time.Second || len(w.state().Tickets) != 0 {
			t.Fatalf("calibration waited %v, tickets %+v", w.waited(), w.state().Tickets)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w.waits = nil
		w.onWait = func(n int) {
			if n == 5 {
				if len(w.state().Tickets) != 1 {
					t.Errorf("a waiting cell has no ticket: %+v", w.state().Tickets)
				}
				cancel()
			}
		}
		next := w.request("claude", workerenv.Worker, workerenv.Free, 913)
		next.Calibration = true
		_, err := w.gate.Acquire(ctx, next)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled wait: %v", err)
		}
		s := w.state()
		if len(s.Tickets) != 0 || len(s.Reservations) != 1 || s.Reservations[p.Reservation.ID].ID == "" {
			t.Fatalf("after cancellation: tickets %+v, reservations %+v", s.Tickets, s.Reservations)
		}
	})
}

package paidruns

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/workerenv"
)

// proc is what the operating system shows at one process ID: whether a
// process runs there and its birth identity, "" when unavailable.
type proc struct {
	state Presence
	birth string
}

// world stands for the processes and panes that reconciliation probes. A
// process ID or pane it does not know is Unknown, as an unreachable service
// or an unreadable process is.
type world struct {
	mu     sync.Mutex
	procs  map[int]proc
	panes  map[string]Presence
	probes int
}

func newWorld() *world {
	return &world{procs: map[int]proc{}, panes: map[string]Presence{}}
}

func (w *world) set(pid int, state Presence, birth string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.procs[pid] = proc{state, birth}
}

func (w *world) pane(id string, state Presence) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.panes[id] = state
}

func (w *world) look(pid int) (Presence, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.probes++
	p := w.procs[pid]
	return p.state, p.birth
}

// input probes the world: processes through the same birth check as the
// platform probe, panes by their ID.
func (w *world) input(now time.Time) ReconcileInput {
	return ReconcileInput{
		Process: func(o Owner) Presence { return processPresence(o, w.look) },
		Pane: func(_ context.Context, o Owner) Presence {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.probes++
			return w.panes[o.Pane]
		},
		Now: now,
	}
}

func mustReconcile(t *testing.T, st *Store, in ReconcileInput) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := st.Reconcile(ctx, in); err != nil {
		t.Fatal(err)
	}
}

// supervisor is the owner that workerTask admits with.
var supervisor = Owner{PID: 4242, Birth: "b-4242"}

// started admits in, marks its launch and attaches child.
func started(t *testing.T, st *Store, in Admission, child Owner) {
	t.Helper()
	ctx := context.Background()
	mustAdmit(t, st, in)
	if err := st.Launch(ctx, in.Reservation.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Attach(ctx, in.Reservation.ID, child); err != nil {
		t.Fatal(err)
	}
}

// peerOpen is a paid peer pane with its first task.
func peerOpen(id string, usd float64) Admission {
	in := paidTask(id, "claude", usd)
	in.Reservation.Shape, in.Assessment.Shape = workerenv.Peer, workerenv.Peer
	return in
}

// wantHeld checks that reservation id is still held, started, and reported
// when report is set, or not reported.
func wantHeld(t *testing.T, st *Store, id string, report bool) Reservation {
	t.Helper()
	r, ok := readState(t, st).Reservations[id]
	if !ok {
		t.Fatalf("%s was freed", id)
	}
	if report && r.Retained == "" || !report && r.Retained != "" {
		t.Fatalf("%s retained with report %q, want report %v", id, r.Retained, report)
	}
	return r
}

func wantGone(t *testing.T, st *Store, id string) {
	t.Helper()
	if r, ok := readState(t, st).Reservations[id]; ok {
		t.Fatalf("%s still held: %+v", id, r)
	}
}

func TestReconcileRetainsLiveChildAndUncertainPeer(t *testing.T) {
	ctx := context.Background()

	t.Run("supervisor gone, child alive", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, paidTask("run", "claude", 1), Owner{PID: 7, Birth: "b-7"})
		w.set(supervisor.PID, Gone, "")
		w.set(7, Alive, "b-7")
		for range 3 {
			mustReconcile(t, st, w.input(now))
		}
		r := wantHeld(t, st, "run", false)
		if !r.Session || !r.Task || !r.Started {
			t.Fatalf("live child's reservation changed: %+v", r)
		}
		wantSpent(t, st, "claude", now, 0, 1)
		// Its task and its slot stay taken.
		mustRefuse(t, st, paidTask("next", "claude", 1), ReasonOneTask)
		one := freeTask("slot", "codex", "personal", now)
		one.TotalLimit = 1
		mustRefuse(t, st, one, ReasonTotalSessions)
	})

	t.Run("reused process ID is not the child", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, paidTask("run", "claude", 1), Owner{PID: 7, Birth: "b-7"})
		w.set(supervisor.PID, Gone, "")
		// Another process was born at the child's ID.
		w.set(7, Alive, "b-7-again")
		mustReconcile(t, st, w.input(now))
		wantGone(t, st, "run")
		if got := charged(t, st, "run"); got != 1 {
			t.Fatalf("charged %v, want the estimate 1", got)
		}
	})

	t.Run("process checks without a conclusion", func(t *testing.T) {
		for name, p := range map[string]proc{
			// The OS refused to show the process.
			"permission denied": {Unknown, ""},
			// It runs, but its birth cannot be read.
			"birth unavailable": {Alive, ""},
			// The probe never answered for this ID.
			"no answer": {},
		} {
			t.Run(name, func(t *testing.T) {
				now := ledgerNow
				st := testStore(t, &now)
				w := newWorld()
				started(t, st, paidTask("run", "claude", 1), Owner{PID: 7, Birth: "b-7"})
				w.set(supervisor.PID, Gone, "")
				w.set(7, p.state, p.birth)
				mustReconcile(t, st, w.input(now))
				wantHeld(t, st, "run", true)
				wantSpent(t, st, "claude", now, 0, 1)
			})
		}
	})

	t.Run("missing child identity", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		// A child recorded without its birth cannot be told from a process
		// that took its ID later.
		started(t, st, paidTask("no-birth", "claude", 1), Owner{PID: 7})
		w.set(supervisor.PID, Gone, "")
		w.set(7, Alive, "b-7")
		// A started record without any child.
		mustAdmit(t, st, paidTask("no-child", "codex", 1))
		if err := st.Update(ctx, func(s *State) error {
			r := s.Reservations["no-child"]
			r.Started = true
			s.Reservations["no-child"] = r
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			mustReconcile(t, st, w.input(now.Add(48*time.Hour)))
		}
		wantHeld(t, st, "no-birth", true)
		wantHeld(t, st, "no-child", true)
		wantSpent(t, st, "claude", now, 0, 1)
		wantSpent(t, st, "codex", now, 0, 1)
	})

	t.Run("pending launch", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		mustAdmit(t, st, paidTask("launching", "claude", 1))
		if err := st.Launch(ctx, "launching"); err != nil {
			t.Fatal(err)
		}
		// The supervisor died between its launch and the attach: a child may
		// run that no record names. No lease expires it.
		w.set(supervisor.PID, Gone, "")
		mustReconcile(t, st, w.input(now.Add(72*time.Hour)))
		wantHeld(t, st, "launching", true)
		wantSpent(t, st, "claude", now.Add(72*time.Hour), 0, 1)
		// A supervisor that cannot be checked proves nothing either.
		unchecked := paidTask("unchecked", "codex", 1)
		unchecked.Reservation.Supervisor = Owner{PID: 5555, Birth: "b-5555"}
		mustAdmit(t, st, unchecked)
		mustReconcile(t, st, w.input(now))
		wantHeld(t, st, "unchecked", true)
	})

	t.Run("pane service unreachable", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, peerOpen("pane", 1), Owner{Pane: "pane-7", PeerSession: "peer-7"})
		w.set(supervisor.PID, Gone, "")
		// No pane probe answers.
		mustReconcile(t, st, w.input(now))
		r := wantHeld(t, st, "pane", true)
		if !r.Session || !r.Task {
			t.Fatalf("unreachable pane lost its hold: %+v", r)
		}
		wantSpent(t, st, "claude", now, 0, 1)
		// Without any pane probe the pane is unknown as well.
		in := w.input(now)
		in.Pane = nil
		mustReconcile(t, st, in)
		wantHeld(t, st, "pane", true)
	})

	t.Run("pane alive after supervisor and lead end", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, peerOpen("pane", 1), Owner{PID: 9, Birth: "b-9", Pane: "pane-7", PeerSession: "peer-7"})
		// The supervisor and the pane's own process ID are gone, yet the
		// pane service shows the pane: the pane decides.
		w.set(supervisor.PID, Gone, "")
		w.set(9, Gone, "")
		w.pane("pane-7", Alive)
		mustReconcile(t, st, w.input(now))
		wantHeld(t, st, "pane", false)
		wantSpent(t, st, "claude", now, 0, 1)
	})

	t.Run("closed pane and gone processes", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, peerOpen("pane", 0.75), Owner{Pane: "pane-7", PeerSession: "peer-7"})
		w.set(supervisor.PID, Gone, "")
		w.pane("pane-7", Gone)
		mustReconcile(t, st, w.input(now))
		wantGone(t, st, "pane")
		if s := readState(t, st); liveSessions(s, "") != 0 {
			t.Fatalf("closed pane still occupies a slot: %+v", s.Reservations)
		}
		// The started task of unknown cost is charged at its estimate, once.
		wantSpent(t, st, "claude", now, 0.75, 0)
		before := readState(t, st)
		for range 3 {
			mustReconcile(t, st, w.input(now))
		}
		mustSettle(t, st, Settlement{ID: "pane", PricedUSD: 3, Started: true, CompleteUsage: true})
		if after := readState(t, st); !reflect.DeepEqual(after, before) {
			t.Fatalf("repeated recovery changed the state:\n%+v\n%+v", before, after)
		}
		wantSpent(t, st, "claude", now, 0.75, 0)
	})

	t.Run("closed pane, supervisor still settling", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, peerOpen("pane", 1), Owner{Pane: "pane-7"})
		w.set(supervisor.PID, Alive, supervisor.Birth)
		w.pane("pane-7", Gone)
		mustReconcile(t, st, w.input(now))
		wantHeld(t, st, "pane", false)
		// The live supervisor settles what it measured, then the closed
		// pane's session ends.
		mustSettle(t, st, Settlement{ID: "pane", PricedUSD: 0.25, Started: true, CompleteUsage: true})
		mustReconcile(t, st, w.input(now))
		wantGone(t, st, "pane")
		wantSpent(t, st, "claude", now, 0.25, 0)
	})

	t.Run("started peer session with settled task", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		// A paid agent runs one task at a time: each settles before the next.
		for i, id := range []string{"closed", "open", "unknown"} {
			started(t, st, peerOpen(id, 1), Owner{Pane: "pane-" + strconv.Itoa(i+1)})
			mustSettle(t, st, Settlement{ID: id, PricedUSD: 0.5, Started: true, CompleteUsage: true})
		}
		w.set(supervisor.PID, Gone, "")
		w.pane("pane-1", Gone)
		w.pane("pane-2", Alive)
		mustReconcile(t, st, w.input(now))
		wantGone(t, st, "closed")
		wantHeld(t, st, "open", false)
		wantHeld(t, st, "unknown", true)
		if s := readState(t, st); liveSessions(s, "claude") != 2 {
			t.Fatalf("sessions %+v", s.Reservations)
		}
		wantSpent(t, st, "claude", now, 1.5, 0)
	})

	t.Run("worker child ended", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, paidTask("paid", "claude", 1.25), Owner{PID: 7, Birth: "b-7"})
		started(t, st, freeTask("free", "codex", "personal", now), Owner{PID: 8, Birth: "b-8"})
		started(t, st, paidTask("settling", "codex", 2), Owner{PID: 10, Birth: "b-10"})
		w.set(7, Gone, "")
		w.set(8, Gone, "")
		w.set(10, Gone, "")
		// Supervisor 4242 died; the "settling" one has a live supervisor
		// of its own that still reads the child's usage.
		if err := st.Update(ctx, func(s *State) error {
			r := s.Reservations["settling"]
			r.Supervisor = Owner{PID: 11, Birth: "b-11"}
			s.Reservations["settling"] = r
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		w.set(supervisor.PID, Gone, "")
		w.set(11, Alive, "b-11")
		mustReconcile(t, st, w.input(now))
		wantGone(t, st, "paid")
		wantGone(t, st, "free")
		wantHeld(t, st, "settling", false)
		// A paid task is charged its estimate; a free one spent its window,
		// which the next quota read shows, and no money.
		wantSpent(t, st, "claude", now, 1.25, 0)
		wantSpent(t, st, "codex", now, 0, 2)
		if got := charged(t, st, "free"); got != 0 {
			t.Fatalf("free task charged %v", got)
		}
		// The supervisor that lived settles its measured cost, not the
		// estimate.
		mustSettle(t, st, Settlement{ID: "settling", PricedUSD: 0.5, Started: true, CompleteUsage: true})
		mustReconcile(t, st, w.input(now))
		wantSpent(t, st, "codex", now, 0.5, 0)
	})

	t.Run("birth check", func(t *testing.T) {
		look := func(state Presence, birth string) func(int) (Presence, string) {
			return func(int) (Presence, string) { return state, birth }
		}
		for _, tc := range []struct {
			name  string
			owner Owner
			look  func(int) (Presence, string)
			want  Presence
		}{
			{"same birth", Owner{PID: 7, Birth: "b"}, look(Alive, "b"), Alive},
			{"reused ID", Owner{PID: 7, Birth: "b"}, look(Alive, "c"), Gone},
			{"no process", Owner{PID: 7, Birth: "b"}, look(Gone, ""), Gone},
			{"no process, no birth recorded", Owner{PID: 7}, look(Gone, ""), Gone},
			{"permission denied", Owner{PID: 7, Birth: "b"}, look(Unknown, ""), Unknown},
			{"birth unreadable", Owner{PID: 7, Birth: "b"}, look(Alive, ""), Unknown},
			{"no birth recorded", Owner{PID: 7}, look(Alive, "b"), Unknown},
			{"no process ID", Owner{Birth: "b"}, look(Alive, "b"), Unknown},
		} {
			if got := processPresence(tc.owner, tc.look); got != tc.want {
				t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
			}
		}
	})

	t.Run("platform probe", func(t *testing.T) {
		cmd, stdin := waitingTask(t)
		pid := cmd.Process.Pid
		o := ProcessOwner(pid)
		if o.PID != pid {
			t.Fatalf("owner %+v", o)
		}
		switch runtime.GOOS {
		case "darwin", "linux", "windows":
			if o.Birth == "" {
				t.Fatalf("no birth identity on %s", runtime.GOOS)
			}
		default:
			t.Skipf("no birth identity on %s", runtime.GOOS)
		}
		if again := ProcessOwner(pid); again != o {
			t.Fatalf("birth identity changed: %+v, %+v", o, again)
		}
		if got := ProbeProcess(o); got != Alive {
			t.Fatalf("running child %v", got)
		}
		if got := ProbeProcess(Owner{PID: pid, Birth: o.Birth + "-other"}); got != Gone {
			t.Fatalf("other birth at the child's ID %v", got)
		}
		if got := ProbeProcess(Owner{PID: pid}); got != Unknown {
			t.Fatalf("child without birth %v", got)
		}
		if got := ProbeProcess(Owner{}); got != Unknown {
			t.Fatalf("no process ID %v", got)
		}
		exitedNormally(t, cmd, stdin)
		if got := ProbeProcess(o); got != Gone {
			t.Fatalf("ended child %v", got)
		}
		if got := ProcessOwner(pid); got.Birth != "" {
			t.Fatalf("ended child's owner %+v", got)
		}
	})
}

func TestReconcileAfterCrashAtEveryTransition(t *testing.T) {
	ctx := context.Background()

	t.Run("crash before launch", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		mustAdmit(t, st, paidTask("never", "claude", 1))
		pane := peerOpen("never-pane", 1)
		pane.Reservation.ID, pane.Reservation.Agent, pane.Assessment.Agent = "never-pane", "codex", "codex"
		mustAdmit(t, st, pane)
		w.set(supervisor.PID, Gone, "")
		mustReconcile(t, st, w.input(now))
		// A dead supervisor that never began a launch proves the task never
		// started: its estimate and slot come back.
		wantGone(t, st, "never")
		wantGone(t, st, "never-pane")
		wantSpent(t, st, "claude", now, 0, 0)
		if s := readState(t, st); len(s.Settled) != 0 || len(s.Reservations) != 0 {
			t.Fatalf("never-started reservations left %+v", s)
		}
		// A live supervisor keeps its unstarted reservation.
		mustAdmit(t, st, paidTask("admitted", "claude", 1))
		w.set(supervisor.PID, Alive, supervisor.Birth)
		mustReconcile(t, st, w.input(now))
		wantHeld(t, st, "admitted", false)
		// So does a supervisor at a reused ID once its launch began.
		if err := st.Launch(ctx, "admitted"); err != nil {
			t.Fatal(err)
		}
		w.set(supervisor.PID, Alive, "b-4242-again")
		mustReconcile(t, st, w.input(now))
		wantHeld(t, st, "admitted", true)
	})

	t.Run("crash after attach", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, paidTask("run", "claude", 1), Owner{PID: 7, Birth: "b-7"})
		w.set(supervisor.PID, Gone, "")
		w.set(7, Alive, "b-7")
		for range 3 {
			mustReconcile(t, st, w.input(now))
			wantHeld(t, st, "run", false)
		}
		wantSpent(t, st, "claude", now, 0, 1)
		// The child ends later; the next recovery charges it once.
		w.set(7, Gone, "")
		for range 3 {
			mustReconcile(t, st, w.input(now))
		}
		wantGone(t, st, "run")
		wantSpent(t, st, "claude", now, 1, 0)
	})

	t.Run("crash after task end", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, paidTask("run", "claude", 1.5), Owner{PID: 7, Birth: "b-7"})
		w.set(supervisor.PID, Gone, "")
		w.set(7, Gone, "")
		mustReconcile(t, st, w.input(now))
		before := readState(t, st)
		if s := before.Settled["run"]; s.ChargedUSD != 1.5 || !s.Started || s.CompleteUsage || !s.Ended.Equal(now) {
			t.Fatalf("recovered settlement %+v", s)
		}
		for range 3 {
			mustReconcile(t, st, w.input(now))
		}
		// A supervisor that comes back late cannot charge again.
		mustSettle(t, st, Settlement{ID: "run", PricedUSD: 4, Started: true, CompleteUsage: true})
		if after := readState(t, st); !reflect.DeepEqual(after, before) {
			t.Fatalf("repeated recovery changed the state:\n%+v\n%+v", before, after)
		}
		wantSpent(t, st, "claude", now, 1.5, 0)
	})

	t.Run("crash after durable settlement", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, paidTask("run", "claude", 1), Owner{PID: 7, Birth: "b-7"})
		mustSettle(t, st, Settlement{ID: "run", PricedUSD: 0.25, Started: true, CompleteUsage: true})
		w.set(supervisor.PID, Gone, "")
		w.set(7, Gone, "")
		before := readState(t, st)
		for range 3 {
			mustReconcile(t, st, w.input(now))
		}
		if after := readState(t, st); !reflect.DeepEqual(after, before) {
			t.Fatalf("recovery after settlement changed the state:\n%+v\n%+v", before, after)
		}
		wantSpent(t, st, "claude", now, 0.25, 0)
	})

	t.Run("old unsettled record stays", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		started(t, st, paidTask("old", "claude", 4.5), Owner{PID: 7, Birth: "b-7"})
		w.set(supervisor.PID, Gone, "")
		later := now.Add(30 * time.Hour)
		now = later
		mustReconcile(t, st, w.input(later))
		wantHeld(t, st, "old", true)
		wantSpent(t, st, "claude", later, 0, 4.5)
		mustRefuse(t, st, paidTask("after-old", "claude", 0.75), ReasonCap)
	})

	t.Run("settled history ages out", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		w := newWorld()
		for id, ended := range map[string]time.Time{
			"day-old":   now.Add(-24 * time.Hour),
			"older":     now.Add(-40 * time.Hour),
			"edge":      now.Add(-24*time.Hour + time.Second),
			"recent":    now.Add(-time.Hour),
			"in-future": now.Add(time.Hour),
		} {
			mustAdmit(t, st, paidTask(id, "claude", 0.5))
			mustSettle(t, st, Settlement{ID: id, PricedUSD: 0.5, Started: true, CompleteUsage: true, Ended: ended})
		}
		started(t, st, paidTask("old-run", "codex", 1), Owner{PID: 7, Birth: "b-7"})
		w.set(7, Alive, "b-7")
		wantSpent(t, st, "claude", now, 1.5, 0)
		mustReconcile(t, st, w.input(now))
		s := readState(t, st)
		for _, id := range []string{"day-old", "older"} {
			if _, ok := s.Settled[id]; ok {
				t.Fatalf("%s kept past the day", id)
			}
		}
		for _, id := range []string{"edge", "recent", "in-future"} {
			if _, ok := s.Settled[id]; !ok {
				t.Fatalf("%s pruned inside the day", id)
			}
		}
		// Pruning changes no spend and frees no reservation.
		wantSpent(t, st, "claude", now, 1.5, 0)
		wantHeld(t, st, "old-run", false)
	})

	t.Run("probes run outside the lock and records are checked again", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		mustAdmit(t, st, paidTask("racing", "claude", 1))
		attached := false
		in := ReconcileInput{
			Process: func(o Owner) Presence {
				// While the supervisor is probed, another process attaches
				// the child: the lock must be free, and the stale probe must
				// not release the now started task.
				if o == supervisor && !attached {
					attached = true
					if err := st.Launch(ctx, "racing"); err != nil {
						t.Error(err)
					}
					if err := st.Attach(ctx, "racing", Owner{PID: 7, Birth: "b-7"}); err != nil {
						t.Error(err)
					}
				}
				return Gone
			},
			Now: now,
		}
		mustReconcile(t, st, in)
		if !attached {
			t.Fatal("the supervisor was never probed")
		}
		r := wantHeld(t, st, "racing", false)
		if !r.Started || r.Child.PID != 7 {
			t.Fatalf("racing reservation %+v", r)
		}
		wantSpent(t, st, "claude", now, 0, 1)
	})

	t.Run("state errors refuse", func(t *testing.T) {
		now := ledgerNow
		st := testStore(t, &now)
		path := filepath.Join(st.Dir, stateFile)
		if err := os.WriteFile(path, []byte(`{"version": 99}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := st.Reconcile(ctx, newWorld().input(now)); err == nil {
			t.Fatal("reconciled a state of another version")
		}
		if data, _ := os.ReadFile(path); string(data) != `{"version": 99}` {
			t.Fatalf("state of another version replaced: %s", data)
		}
	})
}

package paidruns

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/workerenv"
)

// peerOf makes in a peer pane's open: a slot with its first task.
func peerOf(in Admission) Admission {
	in.Reservation.Shape, in.Assessment.Shape = workerenv.Peer, workerenv.Peer
	return in
}

// sendTo makes in a send to session: a peer task without a slot.
func sendTo(in Admission, session string) Admission {
	in = peerOf(in)
	in.Reservation.SessionID, in.Reservation.Session = session, false
	return in
}

// A launch whose supervisor died before the attach stays held, since a
// child may run, unless its launch token, a file the launcher must take to
// start anything, is still there: reconciliation takes it, which proves the
// child never starts, and frees the hold. A token already taken keeps it.
func TestReconcileTakesLaunchToken(t *testing.T) {
	for _, c := range []struct {
		name  string
		taken bool
	}{{"token still there", false}, {"token taken", true}} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			now := ledgerNow
			st := testStore(t, &now)
			w := newWorld()
			token := filepath.Join(t.TempDir(), ".peer-env-1.json")
			if err := os.WriteFile(token, []byte("[]"), 0600); err != nil {
				t.Fatal(err)
			}
			mustAdmit(t, st, peerOf(paidTask("pane", "claude", 1)))
			if err := st.LaunchWithToken(ctx, "pane", token); err != nil {
				t.Fatal(err)
			}
			if c.taken {
				if err := os.Remove(token); err != nil {
					t.Fatal(err)
				}
			}
			w.set(supervisor.PID, Gone, "")
			mustReconcile(t, st, w.input(now))
			if c.taken {
				wantHeld(t, st, "pane", true)
				wantSpent(t, st, "claude", now, 0, 1)
				return
			}
			wantGone(t, st, "pane")
			wantSpent(t, st, "claude", now, 0, 0)
			if _, err := os.Stat(token); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("the token is still there: %v", err)
			}
		})
	}
}

// launcher is the process of duolog peer launch in a pane, which records
// itself on its pane's hold once it took the env file.
var launcher = Owner{PID: 5151, Birth: "b-5151"}

// launchedPane admits pane p1's open, marks its launch with a token that the
// launcher already took and lets its supervisor die: the input of re-review
// finding N2. recorded says whether the launcher recorded itself.
func launchedPane(t *testing.T, st *Store, w *world, recorded bool) {
	t.Helper()
	ctx := context.Background()
	in := peerOf(paidTask("pane", "claude", 1))
	in.Reservation.Peer = "p1"
	mustAdmit(t, st, in)
	if err := st.LaunchWithToken(ctx, "pane", filepath.Join(t.TempDir(), ".peer-env-taken.json")); err != nil {
		t.Fatal(err)
	}
	if recorded {
		if err := st.RecordLauncher(ctx, "p1", launcher); err != nil {
			t.Fatal(err)
		}
	}
	w.set(supervisor.PID, Gone, "")
}

// TestReconcileFindsTheLaunchedPane is re-review finding N2: the launcher
// took the env file and peer open died before it recorded the pane. The
// launcher, the pane's own program, recorded itself on the hold by the peer
// id of its environment, so reconciliation follows it: the hold stays while
// it runs or cannot be checked, and ends, refunded since nothing was sent,
// once it is gone. Without that record the hold stays, for peer release.
func TestReconcileFindsTheLaunchedPane(t *testing.T) {
	for _, c := range []struct {
		name     string
		recorded bool
		state    Presence
		held     bool
	}{
		{"launcher runs", true, Alive, true},
		{"launcher gone", true, Gone, false},
		{"launcher unknown", true, Unknown, true},
		{"no launcher recorded", false, Gone, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			now := ledgerNow
			st := testStore(t, &now)
			w := newWorld()
			launchedPane(t, st, w, c.recorded)
			w.set(launcher.PID, c.state, launcher.Birth)
			mustReconcile(t, st, w.input(now))
			if c.held {
				if r := wantHeld(t, st, "pane", true); r.Peer != "p1" {
					t.Errorf("held reservation lost its peer: %+v", r)
				}
				wantSpent(t, st, "claude", now, 0, 1)
				return
			}
			wantGone(t, st, "pane")
			wantSpent(t, st, "claude", now, 0, 0)
		})
	}
	// A launcher of a pane that holds nothing changes nothing.
	now := ledgerNow
	st := testStore(t, &now)
	if err := st.RecordLauncher(context.Background(), "p9", launcher); err != nil {
		t.Errorf("launcher without a hold: %v", err)
	}
}

// EndHold ends a named hold, by its peer id or its reservation id, as a
// reconciliation would that found its owners gone: a pane's unsent first
// task is refunded. It refuses while the pane, its launcher or the command
// that holds it still runs, and names what does.
func TestReleaseEndsANamedHold(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, id string
		world    func(*world)
		refused  string
	}{
		{"by peer id", "p1", func(*world) {}, ""},
		{"by reservation id", "pane", func(*world) {}, ""},
		{"launcher cannot be checked", "p1", func(w *world) { w.set(launcher.PID, Unknown, "") }, ""},
		{"launcher runs", "p1", func(w *world) { w.set(launcher.PID, Alive, launcher.Birth) }, "its pane still runs"},
		{"supervisor runs", "p1", func(w *world) { w.set(supervisor.PID, Alive, supervisor.Birth) }, "still runs"},
		{"unknown name", "p2", func(*world) {}, "no hold"},
	} {
		t.Run(c.name, func(t *testing.T) {
			now := ledgerNow
			st := testStore(t, &now)
			w := newWorld()
			launchedPane(t, st, w, c.name != "by peer id")
			c.world(w)
			_, err := st.EndHold(ctx, c.id, w.input(now))
			if c.refused != "" {
				if err == nil || !strings.Contains(err.Error(), c.refused) {
					t.Fatalf("release %s: err %v, want %q", c.id, err, c.refused)
				}
				wantHeld(t, st, "pane", false)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantGone(t, st, "pane")
			wantSpent(t, st, "claude", now, 0, 0)
		})
	}

	// A started pane that still runs is refused; a sent task of a pane that
	// is gone counts at its estimate.
	now := ledgerNow
	st := testStore(t, &now)
	w := newWorld()
	open := peerOf(paidTask("pane", "claude", 1))
	open.Reservation.Peer = "p1"
	started(t, st, open, Owner{Pane: "block-1"})
	send := sendTo(paidTask("send", "claude", 1), "pane")
	send.Reservation.Peer = "p1"
	started(t, st, send, Owner{Pane: "block-1"})
	w.set(supervisor.PID, Gone, "")
	w.pane("block-1", Alive)
	if _, err := st.EndHold(ctx, "p1", w.input(now)); err == nil || !strings.Contains(err.Error(), "its pane still runs") {
		t.Fatalf("live pane released: %v", err)
	}
	w.pane("block-1", Unknown)
	ended, err := st.EndHold(ctx, "p1", w.input(now))
	if err != nil || len(ended) != 2 {
		t.Fatalf("release: %v, ended %v", err, ended)
	}
	wantGone(t, st, "pane")
	wantGone(t, st, "send")
	wantSpent(t, st, "claude", now, 1, 0)
}

// TestReportShowsRetainedHolds is re-review finding N2: a hold that
// reconciliation keeps without a conclusion shows in the account lines of
// quota and doctor, by its peer id or else its reservation id, with its
// reason and the command that ends it. A hold kept for a live owner does
// not show.
func TestReportShowsRetainedHolds(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	created := w.now.Add(-time.Hour)
	if err := w.store.Update(ctx, func(st *State) error {
		st.Reservations["pane"] = Reservation{ID: "pane", Agent: "claude", Shape: workerenv.Peer, Peer: "p1", Session: true,
			Task: true, Created: created, Estimate: Estimate{USD: 1.5}, Paid: true,
			Retained: "its supervisor ended during the launch; a child may run unrecorded"}
		st.Reservations["work"] = Reservation{ID: "work", Agent: "codex", Shape: workerenv.Worker, Session: true,
			Task: true, Created: created, Retained: "its process cannot be checked"}
		st.Reservations["live"] = Reservation{ID: "live", Agent: "codex", Shape: workerenv.Worker, Session: true, Created: created}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r, err := w.gate.Report(ctx, w.request("claude", workerenv.Worker, workerenv.Free, 100).Input)
	if r.Assessment.Agent == "" {
		t.Fatal(err)
	}
	if len(r.Held) != 2 {
		t.Fatalf("held %+v, want the two retained holds", r.Held)
	}
	lines := strings.Join(r.AccountLines(), "\n")
	since := created.UTC().Format(time.RFC3339)
	for _, want := range []string{
		"held: p1 (claude peer, since " + since + ", 1.50 USD): its supervisor ended during the launch; a child may run unrecorded; end it with duolog peer release p1",
		"held: work (codex worker, since " + since + "): its process cannot be checked; end it with duolog peer release work",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("missing %q in\n%s", want, lines)
		}
	}
	if strings.Contains(lines, "live") {
		t.Errorf("a live hold shows:\n%s", lines)
	}
}

// An idle pane's held first task does not run: it does not keep the agent's
// paid or workspace delegations to one task. It still counts against the
// cap and the windows, and the pane's first send is held to one task when
// it comes.
func TestIdlePaneDoesNotBlockOneTask(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name       string
		open, work func() Admission
	}{
		{"paid", func() Admission { return peerOf(paidTask("pane", "claude", 1)) },
			func() Admission { return paidTask("work", "claude", 1) }},
		{"workspace", func() Admission { return peerOf(freeTask("pane", "claude", "workspace", ledgerNow)) },
			func() Admission { return freeTask("work", "claude", "workspace", ledgerNow) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			now := ledgerNow
			st := testStore(t, &now)
			mustAdmit(t, st, c.open())
			mustAdmit(t, st, c.work())
			if c.name == "paid" {
				wantSpent(t, st, "claude", now, 0, 2)
			}
			// The pane's first send waits for the delegation's task.
			send := sendTo(c.work(), "pane")
			send.Reservation.ID = "send"
			e, err := st.enter(ctx, send, &line{}, TierResult{}, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if e.Admitted || e.Reason != ReasonOneTask {
				t.Fatalf("first send beside a running task: %+v", e.Decision)
			}
			if r := readState(t, st).Reservations["pane"]; !r.Task {
				t.Fatalf("the refused send took the open's task: %+v", r)
			}
		})
	}
}

package paidruns

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

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

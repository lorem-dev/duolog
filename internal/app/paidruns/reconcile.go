package paidruns

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

// Presence is what a probe knows of a process or a pane: Unknown when it
// cannot tell, Alive, or Gone when it is sure the owner no longer runs.
type Presence int

const (
	Unknown Presence = iota
	Alive
	Gone
)

func (p Presence) String() string {
	switch p {
	case Alive:
		return "alive"
	case Gone:
		return "gone"
	}
	return "unknown"
}

// ReconcileInput holds the probes of a reconciliation and its time. Process
// probes a supervisor or worker process, nil meaning ProbeProcess; Pane
// probes a peer pane through the pane service, nil meaning every pane is
// Unknown. Now is the time of a recovered settlement and of the 24-hour
// history cut, zero meaning the Store's clock.
type ReconcileInput struct {
	Process func(paidrules.Owner) Presence
	Pane    func(context.Context, paidrules.Owner) Presence
	Now     time.Time
}

// Launch records that the supervisor of reservation id is about to start its
// child: a worker process or a peer pane. Call it before the start, so that
// a supervisor dying between the start and Attach leaves the reservation
// held, never proven unstarted. A started reservation, or one launched
// before, is left as it is.
func (s *Store) Launch(ctx context.Context, id string) error {
	return s.LaunchWithToken(ctx, id, "")
}

// LaunchWithToken is Launch with a launch token: a file whose launcher must
// take it, read and remove it, before it starts anything. While the token is
// there to take, nothing started; reconciliation takes it to prove so after
// the supervisor died. "" is no token.
func (s *Store) LaunchWithToken(ctx context.Context, id, token string) error {
	return s.Update(ctx, func(st *State) error {
		r, ok := st.Reservations[id]
		if !ok {
			return fmt.Errorf("paid runs: no reservation %s to launch", id)
		}
		if r.Started || r.Launching {
			return nil
		}
		r.Launching, r.LaunchToken = true, token
		st.Reservations[id] = r
		return nil
	})
}

// outcome is what reconciliation does with one reservation.
type outcome int

const (
	// keep holds the reservation: its owner is alive, or no safe conclusion
	// exists and the reason is reported in Retained.
	keep outcome = iota //nolint:unused // the zero value: conclusion{} keeps, and the switch's default handles it
	// release drops a reservation proven never to have started.
	release
	// end drops a reservation whose child ended, charging an unsettled task
	// at its estimate.
	end
)

type conclusion struct {
	outcome outcome
	reason  string
}

// Reconcile ends the reservations whose owners are proven gone and keeps
// every other one, so a crash at any point neither frees a live child nor
// charges twice:
//
//   - Not started: released when its supervisor is gone and never began a
//     launch. After Launch, a child may run that no record names, so it
//     stays held, unless its launch token is still there to take, or the
//     pane's launcher that took it recorded itself and is gone.
//   - Started: the child decides, a pane by the pane service and a process
//     by its ID and birth identity. The end of the supervisor or the lead
//     says nothing about a pane. A live or uncertain child stays held.
//   - Child gone: a session whose task settled ends. An unsettled task ends
//     once its supervisor is gone too, charged as a run of unknown cost: a
//     paid one at least its estimate. A live supervisor settles what it
//     measured. A pane's first task that its open still holds was never
//     sent and is refunded.
//
// No age ends a reservation. Each one held without a conclusion says why in
// Retained, for quota and doctor to show with the command that ends it
// (EndHold). Settled history older than the
// 24 hours the cap covers is dropped.
//
// The probes run outside the lock, on a copy of the state. The changes are
// made under the lock and only to a reservation still as it was probed, so a
// concurrent attach or settlement is never undone. Run Reconcile before an
// admission: it takes the lock twice and never across a probe.
func (s *Store) Reconcile(ctx context.Context, in ReconcileInput) error {
	if err := s.replaySettlements(ctx); err != nil {
		return err
	}
	if in.Process == nil {
		in.Process = ProbeProcess
	}
	now := in.Now
	if now.IsZero() {
		now = s.now()
	}
	var probed map[string]paidrules.Reservation
	if err := s.Update(ctx, func(st *State) error {
		probed = maps.Clone(st.Reservations)
		return nil
	}); err != nil {
		return err
	}
	found := make(map[string]conclusion, len(probed))
	for _, id := range slices.Sorted(maps.Keys(probed)) {
		found[id] = in.judge(ctx, probed[id])
	}
	return s.Update(ctx, func(st *State) error {
		for id, c := range found {
			r, ok := st.Reservations[id]
			if !ok || !sameRecord(r, probed[id]) {
				continue
			}
			switch c.outcome {
			case release:
				delete(st.Reservations, id)
			case end:
				st.end(r, now)
			default:
				r.Retained = c.reason
				st.Reservations[id] = r
			}
		}
		for id, settled := range st.Settled {
			if now.Sub(settled.Ended) >= capSpan {
				delete(st.Settled, id)
			}
		}
		return nil
	})
}

// end drops r, whose child ended, at now. A pane's open still holding its
// first task never sent it, since a send replaces that hold before anything
// is typed: it is refunded, as CloseSession does. Any other unsettled task
// counts as a run of unknown cost: a paid one at least its estimate.
func (st *State) end(r paidrules.Reservation, now time.Time) {
	unsent := r.Shape == launch.Peer && r.Session
	if _, settled := st.Settled[r.ID]; r.Task && !settled && !unsent {
		recovered := paidrules.Settlement{ID: r.ID, Agent: r.Agent, AccountKey: r.AccountKey, Started: true, Ended: now,
			Windows: r.Estimate.Windows}
		recovered.ChargedUSD = charge(r, recovered)
		st.Settled[r.ID] = recovered
	}
	delete(st.Reservations, r.ID)
}

// RecordLauncher records launcher, the process of duolog peer launch, on the
// hold of the pane whose entry id is peer, its DUOLOG_PEER, while no pane is
// attached to it. The launcher calls it once it took the env file and before
// it starts the agent, so a peer open that dies before it records the pane
// leaves the pane's presence behind. A pane that holds nothing changes
// nothing.
func (s *Store) RecordLauncher(ctx context.Context, peer string, launcher paidrules.Owner) error {
	if peer == "" || launcher.PID <= 0 {
		return errors.New("paid runs: a launcher needs its peer and a process id")
	}
	return s.Update(ctx, func(st *State) error {
		for id, r := range st.Reservations {
			if r.Peer == peer && r.Session && !r.Started && r.Launcher == (paidrules.Owner{}) {
				r.Launcher = launcher
				st.Reservations[id] = r
			}
		}
		return nil
	})
}

// EndHold ends the holds that name names, a peer entry id or a reservation
// id, at the person's word: the command for a hold that reconciliation
// retains. It refuses while any owner of one still runs: its pane, its
// pane's launcher or process, or the command that holds it. One that cannot
// be checked does not refuse, since the person says it is gone. Each hold
// then ends as in a reconciliation that found its owners gone. It returns
// the holds it ended.
func (s *Store) EndHold(ctx context.Context, name string, in ReconcileInput) ([]paidrules.Reservation, error) {
	if in.Process == nil {
		in.Process = ProbeProcess
	}
	now := in.Now
	if now.IsZero() {
		now = s.now()
	}
	var named []paidrules.Reservation
	if err := s.View(ctx, func(st State) error {
		for _, id := range slices.Sorted(maps.Keys(st.Reservations)) {
			if r := st.Reservations[id]; id == name || name != "" && r.Peer == name {
				named = append(named, r)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(named) == 0 {
		return nil, fmt.Errorf("paid runs: no hold named %s", name)
	}
	for _, r := range named {
		if r.Child.Pane != "" && in.Pane != nil && in.Pane(ctx, r.Child) == Alive ||
			r.Child.PID > 0 && in.Process(r.Child) == Alive ||
			r.Launcher.PID > 0 && in.Process(r.Launcher) == Alive {
			return nil, fmt.Errorf("paid runs: %s: its pane still runs; close it first", name)
		}
		if in.Process(r.Supervisor) == Alive {
			return nil, fmt.Errorf("paid runs: %s: the command that holds it still runs (process %d)", name, r.Supervisor.PID)
		}
	}
	var ended []paidrules.Reservation
	err := s.Update(ctx, func(st *State) error {
		for _, probed := range named {
			r, ok := st.Reservations[probed.ID]
			if !ok || !sameRecord(r, probed) {
				continue
			}
			st.end(r, now)
			ended = append(ended, r)
		}
		return nil
	})
	return ended, err
}

// peerID is the peer entry id of the launch in, "" when it is no peer.
func peerID(in launch.ProcessInput) string {
	if in.Shape == launch.Peer {
		return in.PeerID
	}
	return ""
}

// judge concludes what to do with r from its owners' presence.
func (in ReconcileInput) judge(ctx context.Context, r paidrules.Reservation) conclusion {
	if !r.Started && r.Child == (paidrules.Owner{}) {
		switch in.Process(r.Supervisor) {
		case Alive:
			return conclusion{}
		case Gone:
			if !r.Launching {
				return conclusion{outcome: release}
			}
			// A token still there to take proves that nothing started, and
			// taking it keeps the launcher from starting anything.
			if r.LaunchToken != "" && os.Remove(r.LaunchToken) == nil {
				return conclusion{outcome: release}
			}
			// A launcher that took the token is the pane's program: the
			// pane lives while it does.
			if r.Launcher.PID > 0 {
				switch in.Process(r.Launcher) {
				case Alive:
					return conclusion{reason: "its pane runs, but no peer entry records it"}
				case Gone:
					return conclusion{outcome: end}
				}
				return conclusion{reason: "its pane's launcher cannot be checked"}
			}
			return conclusion{reason: "its supervisor ended during the launch; a child may run unrecorded"}
		}
		return conclusion{reason: "its supervisor cannot be checked"}
	}
	child, what := Unknown, "its child has no identity"
	switch {
	case r.Child.Pane != "":
		what = "its pane cannot be checked"
		if in.Pane != nil {
			child = in.Pane(ctx, r.Child)
		}
	case r.Child.PID > 0:
		what = "its process cannot be checked"
		child = in.Process(r.Child)
	}
	switch child {
	case Alive:
		return conclusion{}
	case Unknown:
		return conclusion{reason: what}
	}
	if !r.Task {
		return conclusion{outcome: end}
	}
	switch in.Process(r.Supervisor) {
	case Alive:
		// The supervisor settles what it measured.
		return conclusion{}
	case Gone:
		return conclusion{outcome: end}
	}
	return conclusion{reason: "its child ended, but its supervisor cannot be checked"}
}

// sameRecord reports whether r is still the reservation that was probed: the
// same owners in the same stage.
func sameRecord(r, probed paidrules.Reservation) bool {
	return r.Supervisor == probed.Supervisor && r.Child == probed.Child && r.Launcher == probed.Launcher &&
		r.Started == probed.Started && r.Launching == probed.Launching &&
		r.Task == probed.Task && r.Session == probed.Session &&
		r.SessionID == probed.SessionID && r.Created.Equal(probed.Created)
}

// ProbeProcess reports whether process o runs: Alive only when a process
// runs at its ID with its birth identity, Gone when none runs there or one
// with another birth does. Without an ID, a recorded birth or a readable
// one, it is Unknown.
func ProbeProcess(o paidrules.Owner) Presence {
	return processPresence(o, lookProcess)
}

// ProcessOwner is the process pid as a reservation records it: its ID and
// birth identity, which stays empty when the platform does not show it.
func ProcessOwner(pid int) paidrules.Owner {
	state, birth := lookProcess(pid)
	if state != Alive {
		birth = ""
	}
	return paidrules.Owner{PID: pid, Birth: birth}
}

// processPresence judges o from look, which reports whether a process runs
// at an ID and its birth identity, "" when unavailable. A permission refusal
// that hides the birth is Unknown, not Gone.
func processPresence(o paidrules.Owner, look func(pid int) (Presence, string)) Presence {
	if o.PID <= 0 {
		return Unknown
	}
	state, birth := look(o.PID)
	switch state {
	case Gone:
		return Gone
	case Alive:
		switch {
		case o.Birth == "" || birth == "":
			return Unknown
		case birth != o.Birth:
			// The ID was given to another process: this one ended.
			return Gone
		}
		return Alive
	}
	return Unknown
}

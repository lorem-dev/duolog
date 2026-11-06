package paidruns

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

// ReasonQueue refuses a request because an earlier waiter has the claim on
// the slot it needs. The request may wait for its turn.
const ReasonQueue = "an earlier request waits for the slot"

// slotReason reports whether a refusal is for a slot or the one-task rule,
// which free up as other runs end, so the request may wait.
func slotReason(reason string) bool {
	switch reason {
	case ReasonAgentSessions, ReasonTotalSessions, ReasonOneTask, ReasonQueue:
		return true
	}
	return false
}

// line is a request's place in the queue: its ticket, "" while it holds
// none, its process, its deadline, zero for none, and whether it may wait.
type line struct {
	ticket  string
	owner   paidrules.Owner
	expires time.Time
	wait    bool
}

// entry is the outcome of one locked admission attempt: the decision and the
// sessions held after it, of the agent and of all agents.
type entry struct {
	paidrules.Decision
	agentLive, totalLive int
}

// enter decides in under the state's lock, in queue order, and writes the
// outcome. Tickets past their deadline go first, and those of waiters
// proven gone (gone, by ticket ID and owner). An admission that an earlier
// waiter has the claim on is refused with ReasonQueue. Admitted, the
// reservation is written, and its ticket goes; refused for a slot, the
// request takes a ticket at the back of the queue when it may wait and has
// none, and otherwise leaves it. The agent's tier is recorded with its live
// sessions.
//
// A peer send whose session still holds the open's task is admitted afresh,
// on the cap, windows, one task and assessment as they are now, with that
// hold set aside; admitted, it replaces the hold, so nothing is reserved
// twice, and refused, the open keeps it. The open keeps its slot either way.
//
// dry decides without writing anything but the dropped tickets, to learn
// whether a waiter's turn has come.
func (s *Store) enter(ctx context.Context, in Admission, l *line, tier paidrules.TierResult,
	gone map[string]paidrules.Owner, dry bool) (entry, error) {
	r, err := in.prepare()
	if err != nil {
		return entry{}, err
	}
	var e entry
	err = s.Update(ctx, func(st *State) error {
		now := s.now()
		st.pruneTickets(now, gone)
		seq, queued := st.ticketSequence(l.ticket)
		if !queued {
			l.ticket = ""
		}
		// A peer send whose session's open still holds the first task is
		// decided without that hold, so it is not counted twice; admitted, it
		// replaces the hold, and refused, the hold stays as it was.
		var restore func()
		if r.Shape == launch.Peer && !r.Session {
			var err error
			if restore, err = st.setAsideOpenTask(r); err != nil {
				return err
			}
		}
		d, err := st.decide(in, r, now)
		if err != nil {
			return err
		}
		if d.Admitted && st.ahead(d.Reservation, seq, in) {
			live, _ := st.sessions(r.Agent)
			d = paidrules.Decision{Reservation: d.Reservation, Reason: ReasonQueue, Live: live, Limit: in.AgentLimit}
		}
		if restore != nil && (!d.Admitted || dry) {
			restore()
		}
		if d.Admitted && !dry {
			st.Reservations[r.ID] = d.Reservation
		}
		agentLive, totalLive := st.sessions(r.Agent)
		if dry && d.Admitted && r.Session {
			agentLive, totalLive = agentLive+1, totalLive+1
		}
		e = entry{Decision: d, agentLive: agentLive, totalLive: totalLive}
		if dry {
			return nil
		}
		// A tier made for an account the state no longer holds is not kept.
		if tier.Tier != "" && d.Reason != ReasonAccountChanged {
			tier.Live = agentLive
			st.Tiers[r.Agent] = tier
		}
		switch {
		case d.Admitted || !l.wait || !slotReason(d.Reason):
			st.dropTicket(l.ticket)
			l.ticket = ""
		case l.ticket == "":
			st.NextTicket++
			st.Tickets = append(st.Tickets, paidrules.Ticket{ID: r.ID, Agent: r.Agent, Sequence: st.NextTicket,
				Created: now, Owner: l.owner, Expires: l.expires, Step: r.Step})
			l.ticket = r.ID
		}
		return nil
	})
	if err != nil {
		return entry{}, err
	}
	return e, nil
}

// setAsideOpenTask takes the first task off the open of peer send r's
// session, if it still holds one, so that the send is decided on the state
// without it; restore puts it back. A send of another variant than its
// session's is an error.
func (st *State) setAsideOpenTask(r paidrules.Reservation) (restore func(), err error) {
	for _, id := range slices.Sorted(maps.Keys(st.Reservations)) {
		open := st.Reservations[id]
		if !open.Session || open.Agent != r.Agent || sessionKey(open) != r.SessionID {
			continue
		}
		if open.Variant != r.Variant {
			return nil, fmt.Errorf("paid runs: session %s runs the %s variant, not %s", r.SessionID, open.Variant, r.Variant)
		}
		if !open.Task {
			return nil, nil
		}
		aside := open
		aside.Task, aside.Estimate = false, paidrules.Estimate{}
		st.Reservations[id] = aside
		return func() { st.Reservations[id] = open }, nil
	}
	return nil, nil
}

// ahead reports whether waiters older than sequence seq have the claim on
// what r needs. A waiter of the same agent always does: the agent's slots
// and its one task go in arrival order. A waiter of another agent claims a
// slot of the total only while its agent has a slot of its own free, by
// the limit its tier last recorded; one whose agent is full could not use
// it. r needs a session when it has one; a paid or workspace task also
// waits behind the agent's earlier waiters for the one task.
func (st *State) ahead(r paidrules.Reservation, seq uint64, in Admission) bool {
	agentLive, totalLive := st.sessions(r.Agent)
	same, other := 0, 0
	for _, t := range st.Tickets {
		switch {
		case t.Sequence >= seq:
		case t.Agent == r.Agent:
			same++
		default:
			if limit := st.Tiers[t.Agent].Limit; limit > 0 {
				if live, _ := st.sessions(t.Agent); live >= limit {
					continue
				}
			}
			other++
		}
	}
	if r.Session && (agentLive+same >= in.AgentLimit || totalLive+same+other >= in.TotalLimit) {
		return true
	}
	return r.Task && (r.Paid || r.Workspace) && same > 0
}

// pruneTickets drops the tickets whose deadline passed before now and those
// of gone, a ticket ID with the owner a probe found gone, while the ticket
// still has that owner.
func (st *State) pruneTickets(now time.Time, gone map[string]paidrules.Owner) {
	st.Tickets = slices.DeleteFunc(st.Tickets, func(t paidrules.Ticket) bool {
		if owner, ok := gone[t.ID]; ok && owner == t.Owner {
			return true
		}
		return !t.Expires.IsZero() && now.After(t.Expires)
	})
}

// ticketSequence is the sequence of ticket id. Without one, every ticket is
// older than the request.
func (st *State) ticketSequence(id string) (uint64, bool) {
	if id != "" {
		for _, t := range st.Tickets {
			if t.ID == id {
				return t.Sequence, true
			}
		}
	}
	return math.MaxUint64, false
}

func (st *State) dropTicket(id string) {
	if id == "" {
		return
	}
	st.Tickets = slices.DeleteFunc(st.Tickets, func(t paidrules.Ticket) bool { return t.ID == id })
}

// goneWaiters probes the owners of tickets other than own, outside the
// lock, and returns those proven gone. An owner that cannot be checked
// keeps its ticket.
func goneWaiters(tickets []paidrules.Ticket, own string, probe func(paidrules.Owner) Presence) map[string]paidrules.Owner {
	gone := map[string]paidrules.Owner{}
	for _, t := range tickets {
		if t.ID != own && probe(t.Owner) == Gone {
			gone[t.ID] = t.Owner
		}
	}
	return gone
}

// leave drops l's ticket, also after ctx ended: a canceled waiter leaves
// the queue rather than holding others back.
func (s *Store) leave(ctx context.Context, l *line) error {
	if l.ticket == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaveTimeout)
	defer cancel()
	err := s.Update(ctx, func(st *State) error {
		st.dropTicket(l.ticket)
		return nil
	})
	if err == nil {
		l.ticket = ""
	}
	return err
}

// leaveTimeout bounds how long a waiter that ends tries to leave the queue.
const leaveTimeout = 10 * time.Second

package paidruns

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Admission is one request for a session, a task or both: the reservation
// to hold, the verdict of its process, the agent's daily cap, 0 when paid
// runs are not allowed, and the session limits of all agents together and
// of this agent. ReadAt is when the quota read behind the window costs was
// made: a task of the agent that settled after it still holds its window
// costs, since that read could not show them. Zero counts every settled
// task of the same windows.
type Admission struct {
	Reservation Reservation
	Assessment  Assessment
	DailyCapUSD float64
	TotalLimit  int
	AgentLimit  int
	ReadAt      time.Time
	// accountKey, when set, is the account key the estimate and tier were
	// made for; admission refuses when the state now holds another.
	accountKey *string
}

// Decision is the answer to an Admission. An admitted Reservation is in the
// state before the caller sees it. Reason names why a request was refused;
// Live and Limit are the sessions of the limit that decided, by default the
// agent's.
type Decision struct {
	Admitted    bool
	Reservation Reservation
	Reason      string
	Live, Limit int
}

// Reasons of a refused Decision. A caller may wait for a slot on
// ReasonAgentSessions, ReasonTotalSessions and ReasonOneTask; the others
// make the candidate unavailable.
const (
	ReasonFreeVariantPaid = "the free variant is paid"
	ReasonNotAllowed      = "paid runs are not allowed"
	ReasonCap             = "daily cap reached"
	ReasonWindow          = "no window headroom"
	ReasonOneTask         = "one task at a time"
	ReasonNoSession       = "no open session"
	ReasonAgentSessions   = "agent sessions full"
	ReasonTotalSessions   = "all sessions full"
	// ReasonAccountChanged refuses an admission whose estimate and tier were
	// made for an account the state no longer holds; the gate reads again.
	ReasonAccountChanged = "the account changed during admission"
)

// usdEpsilon absorbs float rounding, so a request that lands exactly on the
// cap is admitted.
const usdEpsilon = 1e-9

// Admit decides in under the state's lock and, when it admits, writes the
// reservation before it returns, so two processes of any projects can never
// both take the last of the cap, a window or a session slot. A refusal
// writes nothing. Every check runs again here, on the state as it is now:
//
//   - The verdict: a paid verdict runs only in the paid variant, and the paid
//     variant only with a daily cap above 0.
//   - A paid task: the agent's settled spend of the last 24 hours, the
//     estimates of its paid tasks in flight, whatever their age, and this
//     estimate stay at or below the cap. Spend is the agent's, across
//     projects and account identities.
//   - Any other task: in each of its windows, used, reserved and cost over
//     the usable share, plus the reserve, stay at or below 1. Reserved is at
//     least what the agent's tasks in flight hold there, and those that
//     settled after the read. No window means no headroom.
//   - The account: an admission made for one account key is refused when
//     the state now holds another, since its estimate and tier described
//     the old account.
//   - One task at a time: a paid task waits for the agent's other paid task,
//     and any task of a workspace account for the agent's other task. An
//     idle pane's first task, held by its open until a send takes it, runs
//     nothing and keeps no task waiting; the send is held to the rule.
//   - A new session needs a free slot of the agent and of all agents; a
//     task alone, such as a peer send, needs its open session.
//
// Admit never reads a vendor or runs anything under the lock; the caller
// passes the assessment and the window costs it read before. Invalid input
// and state that cannot be read are errors, and nothing is admitted.
func (s *Store) Admit(ctx context.Context, in Admission) (Decision, error) {
	r, err := in.prepare()
	if err != nil {
		return Decision{}, err
	}
	var d Decision
	err = s.Update(ctx, func(st *State) error {
		var err error
		if d, err = st.decide(in, r, s.now()); err != nil {
			return err
		}
		if d.Admitted {
			st.Reservations[r.ID] = d.Reservation
		}
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	return d, nil
}

// prepare validates in and returns its reservation as admission records it:
// not started, its paid and workspace flags from the assessment, and no
// estimate without a task.
func (in Admission) prepare() (Reservation, error) {
	if err := in.validate(); err != nil {
		return Reservation{}, err
	}
	r := in.Reservation
	r.AccountKey, r.Child, r.Launching, r.Retained, r.LaunchToken, r.Launcher = "", Owner{}, false, "", "", Owner{}
	r.Paid = in.Assessment.Verdict == Paid
	r.Workspace = in.Assessment.Account == "workspace"
	if !r.Task {
		r.Estimate = Estimate{}
	}
	return r, nil
}

// decide decides the prepared r under the lock, at now, without writing it.
// An ID held or settled before is an error.
func (st *State) decide(in Admission, r Reservation, now time.Time) (Decision, error) {
	if _, ok := st.Reservations[r.ID]; ok {
		return Decision{}, fmt.Errorf("paid runs: reservation %s is already held", r.ID)
	}
	if _, ok := st.Settled[r.ID]; ok {
		return Decision{}, fmt.Errorf("paid runs: reservation %s is already settled", r.ID)
	}
	r.AccountKey = st.AccountKeys[r.Agent+"/"+quota.IdentityAccount]
	r.Created = now
	if in.accountKey != nil && *in.accountKey != r.AccountKey {
		live, _ := st.sessions(r.Agent)
		return Decision{Reservation: r, Reason: ReasonAccountChanged, Live: live, Limit: in.AgentLimit}, nil
	}
	return st.admit(in, r), nil
}

// admit decides r against the state without changing it.
func (st *State) admit(in Admission, r Reservation) Decision {
	agentLive, totalLive := st.sessions(r.Agent)
	refuse := func(reason string) Decision {
		return Decision{Reservation: r, Reason: reason, Live: agentLive, Limit: in.AgentLimit}
	}
	a := in.Assessment
	switch {
	case r.Paid && r.Variant != workerenv.Paid:
		return refuse(ReasonFreeVariantPaid)
	case r.Variant == workerenv.Paid && in.DailyCapUSD <= 0:
		return refuse(ReasonNotAllowed)
	case !r.Session && !st.holdsSession(r.Agent, r.SessionID):
		return refuse(ReasonNoSession)
	}
	if r.Task {
		if r.Paid {
			settled, inflight := st.spent(r.Agent, r.Created)
			if settled+inflight+r.Estimate.USD > in.DailyCapUSD+usdEpsilon {
				return refuse(ReasonCap)
			}
		} else if !st.windowsFit(r, a.UsableShare, in.ReadAt) {
			return refuse(ReasonWindow)
		}
		for _, held := range st.Reservations {
			// A pane's open holds its first task until a send takes it; an idle
			// pane runs nothing, so it keeps no other task waiting.
			idle := held.Shape == workerenv.Peer && held.Session
			if held.Agent == r.Agent && held.Task && !idle && (r.Paid && held.Paid || r.Workspace || held.Workspace) {
				return refuse(ReasonOneTask)
			}
		}
	}
	if !r.Session {
		return Decision{Admitted: true, Reservation: r, Live: agentLive, Limit: in.AgentLimit}
	}
	if agentLive >= in.AgentLimit {
		return refuse(ReasonAgentSessions)
	}
	if totalLive >= in.TotalLimit {
		d := refuse(ReasonTotalSessions)
		d.Live, d.Limit = totalLive, in.TotalLimit
		return d
	}
	return Decision{Admitted: true, Reservation: r, Live: agentLive + 1, Limit: in.AgentLimit}
}

// sessions counts the live sessions of agent and of all agents.
func (st *State) sessions(agent string) (agentLive, totalLive int) {
	for _, r := range st.Reservations {
		if r.Session {
			totalLive++
			if r.Agent == agent {
				agentLive++
			}
		}
	}
	return agentLive, totalLive
}

// holdsSession reports whether agent holds the session id: a session
// reservation with that SessionID, or with that ID when it has none.
func (st *State) holdsSession(agent, id string) bool {
	if id == "" {
		return false
	}
	for _, r := range st.Reservations {
		if r.Session && r.Agent == agent && sessionKey(r) == id {
			return true
		}
	}
	return false
}

func sessionKey(r Reservation) string {
	if r.SessionID != "" {
		return r.SessionID
	}
	return r.ID
}

// windowsFit reports whether r's task fits each of its windows at usable
// share u, by FitsWindows, with reserved at least what the agent's tasks in
// flight hold there and what its tasks that settled after readAt held: the
// read could not show their use yet.
func (st *State) windowsFit(r Reservation, u float64, readAt time.Time) bool {
	costs := make([]WindowCost, 0, len(r.Estimate.Windows))
	for _, w := range r.Estimate.Windows {
		held := 0.0
		add := func(ws []WindowCost) {
			for _, o := range ws {
				if sameWindowKey(o.Key, w.Key) {
					held += o.Cost
				}
			}
		}
		for _, other := range st.Reservations {
			if other.Agent == r.Agent && other.Task {
				add(other.Estimate.Windows)
			}
		}
		for _, s := range st.Settled {
			if s.Agent == r.Agent && s.Ended.After(readAt) {
				add(s.Windows)
			}
		}
		w.Reserved = max(w.Reserved, held)
		costs = append(costs, w)
	}
	return FitsWindows(costs, u)
}

func sameWindowKey(a, b WindowKey) bool {
	return a.Kind == b.Kind && a.Family == b.Family && a.Reset.Equal(b.Reset)
}

// validate refuses a request that no state could admit, before the lock.
func (in Admission) validate() error {
	r, a := in.Reservation, in.Assessment
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if r.ID == "" || r.Agent == "" {
		bad("a reservation needs an id and an agent")
	}
	if a.Agent != r.Agent || a.Shape != r.Shape || a.Variant != r.Variant {
		bad("the assessment is of %s %s %s, not of the reservation's %s %s %s",
			a.Agent, a.Shape, a.Variant, r.Agent, r.Shape, r.Variant)
	}
	if r.Shape != workerenv.Worker && r.Shape != workerenv.Peer {
		bad("shape %q is never admitted", r.Shape)
	}
	if r.Variant != workerenv.Free && r.Variant != workerenv.Paid {
		bad("unknown variant %q", r.Variant)
	}
	if a.Verdict != Subscription && a.Verdict != SubscriptionOverage && a.Verdict != Paid {
		bad("unknown verdict %q", a.Verdict)
	}
	if !(a.UsableShare > 0 && a.UsableShare <= 1) {
		bad("usable share must be above 0 and at most 1")
	}
	if !(in.DailyCapUSD >= 0 && in.DailyCapUSD <= math.MaxFloat64) {
		bad("the daily cap must be a finite number of USD, 0 or more")
	}
	if in.TotalLimit <= 0 || in.AgentLimit <= 0 {
		bad("session limits must be above 0")
	}
	if !r.Session && !r.Task {
		bad("a reservation holds a session, a task or both")
	}
	if r.Started || r.Child != (Owner{}) {
		bad("a new reservation has not started")
	}
	if r.Task {
		if err := r.Estimate.validate(); err != nil {
			errs = append(errs, err)
		}
		if a.Verdict == Paid && r.Estimate.USD <= 0 {
			bad("a paid task needs a USD estimate above 0")
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("paid runs: admission refused: %w", err)
	}
	return nil
}

func (e Estimate) validate() error {
	if e.Input < 0 || e.Cached < 0 || e.Output < 0 || e.Cached > e.Input {
		return errors.New("estimate tokens must be 0 or more, cached at most input")
	}
	if !finiteAmount(e.USD) {
		return errors.New("the USD estimate must be a finite number, 0 or more")
	}
	for _, w := range e.Windows {
		if !finiteAmount(w.Used) || !finiteAmount(w.Reserved) || !finiteAmount(w.Cost) || !finiteAmount(w.Reserve) {
			return fmt.Errorf("window %s: shares must be finite numbers, 0 or more", w.Key.Kind)
		}
	}
	return nil
}

// finiteAmount reports whether v is a finite number, 0 or more. NaN fails
// every comparison, so it fails here too.
func finiteAmount(v float64) bool {
	return v >= 0 && v <= math.MaxFloat64
}

// Attach records that reservation id started, and its child: the worker
// process or the peer pane. It is durable before the caller waits on the
// child, so a supervisor that dies afterwards leaves an owner to check.
// Attaching the same child again changes nothing; another child is refused.
func (s *Store) Attach(ctx context.Context, id string, child Owner) error {
	if child.PID <= 0 && child.Pane == "" {
		return errors.New("paid runs: a child needs a process id or a pane")
	}
	return s.Update(ctx, func(st *State) error {
		r, ok := st.Reservations[id]
		if !ok {
			return fmt.Errorf("paid runs: no reservation %s to attach", id)
		}
		if r.Child != (Owner{}) && r.Child != child {
			return fmt.Errorf("paid runs: reservation %s already has a child", id)
		}
		r.Child, r.Started = child, true
		st.Reservations[id] = r
		return nil
	})
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

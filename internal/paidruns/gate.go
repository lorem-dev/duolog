package paidruns

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Reasons of a Refusal beyond those of a Decision.
const (
	ReasonUnpriced    = "no valid price"
	ReasonCalibration = "paid calibration is not allowed"
)

const (
	// pollInterval is how often a waiter checks whether its turn came. A
	// check reads the account state only; the quota is read again only
	// when a slot is free for it.
	pollInterval = time.Second
	// defaultTotalSessions applies when the config has no total.
	defaultTotalSessions = 10
)

// Request asks for one launch: a worker run, a calibration cell, a peer pane
// with its first task (OpenPeer) or a task in an open pane
// (ExistingSession, the session's reservation ID). Input describes the
// process; Family, Role, Class and Records estimate the task; Owner is the
// requesting process, the reservation's supervisor.
//
// Wait is how long to wait for a slot: negative without a deadline, zero
// not at all. A pane open never waits, since a pane stays open long, and a
// calibration cell waits until a slot frees or ctx ends, whatever Wait says.
// A delegation passes SessionWait.
type Request struct {
	Input                                workerenv.ProcessInput
	Family, Role, Class, ExistingSession string
	Records                              []journal.Delegation
	Owner                                Owner
	Calibration, OpenPeer                bool
	Wait                                 time.Duration
}

// Permit is an admitted launch: the process to run, built for this
// admission, its assessment and the reservation already in the account
// state. Before starting the child, call Store.Launch, then Store.Attach
// with the child, and end it with Finish, or CloseSession for a pane.
type Permit struct {
	Process     workerenv.Process
	Assessment  Assessment
	Reservation Reservation
}

// Gate is the one admission path of every worker run, calibration cell and
// peer pane or send. Store is the user's account state, Cache the
// route-aware quota cache and Config the merged config, used for every build
// in place of Input.Config. Reconcile probes owners before each grant, its
// Now taken from Now when zero. Now is the clock and Wait sleeps between
// checks of a waiting request; nil means time.Now and a timer that ends
// with ctx. Layers reads the agent's settings layers for each build; nil
// keeps Input.Layers.
type Gate struct {
	Store     *Store
	Cache     *Cache
	Config    *config.Config
	Reconcile ReconcileInput
	Now       func() time.Time
	Wait      func(context.Context, time.Duration) error
	Layers    func(context.Context, workerenv.ProcessInput) ([]workerenv.LayerFacts, error)
}

// Refusal says why the gate did not admit a request, with names and numbers
// only: the reason and a safe detail, the verdict and its signal names, the
// task's estimate and its source, the agent's sessions, limit and tier, the
// sessions of all agents and how long the request waited. It never holds an
// environment, settings or a route value.
type Refusal struct {
	Agent                 string
	Shape                 workerenv.Shape
	Variant               workerenv.Variant
	Reason, Detail        string
	Verdict               Verdict
	Signals               []workerenv.Signal
	EstimateUSD           float64
	EstimateSource        string
	Tier                  string
	Live, Limit           int
	TotalLive, TotalLimit int
	Waited                time.Duration
}

// Slot reports whether a slot or the one-task rule refused the request:
// another candidate may be tried, or the request may wait.
func (r *Refusal) Slot() bool { return slotReason(r.Reason) }

func (r *Refusal) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (%s variant): %s", r.Agent, r.Shape, r.Variant, r.Reason)
	if r.Detail != "" {
		b.WriteString(": " + r.Detail)
	}
	switch {
	case r.Verdict == Paid && len(r.Signals) > 0:
		names := make([]string, 0, len(r.Signals))
		for _, s := range r.Signals {
			names = append(names, s.Name+" ("+s.Layer+")")
		}
		b.WriteString("; paid: " + strings.Join(names, ", "))
	case r.Verdict == SubscriptionOverage:
		b.WriteString("; paid overage is on; duolog stops before it")
	}
	if r.Limit > 0 {
		fmt.Fprintf(&b, "; %s sessions %d of %d (tier %s)", r.Agent, r.Live, r.Limit, r.Tier)
		if r.Waited > 0 {
			fmt.Fprintf(&b, "; waited %d s", int(r.Waited.Round(time.Second)/time.Second))
		}
		fmt.Fprintf(&b, "; sessions %d of %d in all", r.TotalLive, r.TotalLimit)
	}
	if r.EstimateSource != "" {
		fmt.Fprintf(&b, "; estimate %.2f USD (%s)", r.EstimateUSD, r.EstimateSource)
	}
	if r.Shape == workerenv.Peer && r.Slot() && r.Reason != ReasonOneTask {
		b.WriteString("; close an open pane with duolog peer close first")
	}
	return b.String()
}

// SessionWait is how long a delegation waits for a slot:
// limits.session_wait_seconds, 60 s by default.
func SessionWait(cfg *config.Config) time.Duration {
	if cfg == nil {
		return 0
	}
	return time.Duration(max(cfg.Limits.SessionWaitSeconds, 0)) * time.Second
}

// Acquire admits req or says why not. Each attempt first reconciles the
// account state, so owners proven gone free their slots, then builds the
// process and assesses its quota read outside the lock, estimates the task
// and its window costs, selects the agent's tier, and admits under the lock,
// in queue order, where cap, windows, one task and slots are checked again.
//
// A request refused for a slot may wait: it takes a durable ticket and
// checks the account state on each poll; when its turn comes it rebuilds
// the process and reads the quota afresh before admission. A request that
// no longer fits, for headroom, the cap or the opt-in, is refused and
// launches nothing. The lock is never held while waiting. A request that
// ends, by refusal, deadline or cancellation, leaves the queue.
//
// A paid verdict is admitted only in the paid variant of an agent that
// opted in with a cap; for a calibration cell, also with calibrate. A failed
// read is paid. A run that used overage keeps the next admissions at
// subscription_overage until a successful read made after it. A refusal is
// a *Refusal; other errors mean the state or the build failed.
func (g *Gate) Acquire(ctx context.Context, req Request) (Permit, error) {
	if err := g.check(req); err != nil {
		return Permit{}, err
	}
	wait := req.Wait
	switch {
	case req.OpenPeer:
		wait = 0
	case req.Calibration:
		wait = -1
	}
	id, err := newReservationID()
	if err != nil {
		return Permit{}, err
	}
	start := g.now()
	l := &line{owner: req.Owner, wait: wait != 0}
	if wait > 0 {
		l.expires = start.Add(wait)
	}
	permit, last, refusal, err := g.attempt(ctx, req, id, l, false)
	for {
		if err != nil {
			return Permit{}, errors.Join(err, g.Store.leave(ctx, l))
		}
		if refusal == nil {
			return permit, nil
		}
		now := g.now()
		if !refusal.Slot() || wait == 0 || wait > 0 && !now.Before(l.expires) {
			refusal.Waited = now.Sub(start)
			if err := g.Store.leave(ctx, l); err != nil {
				return Permit{}, err
			}
			return Permit{}, refusal
		}
		step := pollInterval
		if wait > 0 {
			step = min(step, l.expires.Sub(now))
		}
		if err := g.wait(ctx, step); err != nil {
			return Permit{}, errors.Join(fmt.Errorf("paid runs: waiting for a %s session: %w", req.Input.Agent, err),
				g.Store.leave(ctx, l))
		}
		var e entry
		if e, err = g.turn(ctx, last, l); err == nil {
			if e.Admitted || !slotReason(e.Reason) {
				permit, last, refusal, err = g.attempt(ctx, req, id, l, true)
			} else {
				refusal.Reason, refusal.Live, refusal.TotalLive = e.Reason, e.agentLive, e.totalLive
			}
		}
	}
}

// check refuses a request that no state could admit.
func (g *Gate) check(req Request) error {
	in := req.Input
	switch {
	case g.Store == nil || g.Cache == nil || g.Config == nil:
		return errors.New("paid runs: the gate needs a store, a cache and a config")
	case in.Shape == workerenv.Lead:
		return errors.New("paid runs: the lead is never admitted or launched; it is only reported")
	case in.Shape != workerenv.Worker && in.Shape != workerenv.Peer:
		return fmt.Errorf("paid runs: unknown shape %q", in.Shape)
	case in.Agent != "claude" && in.Agent != "codex":
		return fmt.Errorf("paid runs: unknown agent %q", in.Agent)
	case req.Family == "":
		return errors.New("paid runs: a request needs the model family")
	case in.Shape == workerenv.Worker && (req.OpenPeer || req.ExistingSession != ""):
		return errors.New("paid runs: a worker opens no pane and joins no session")
	case in.Shape == workerenv.Peer && req.OpenPeer == (req.ExistingSession != ""):
		return errors.New("paid runs: a peer request either opens a pane or sends to an open session")
	case req.Calibration && in.Shape != workerenv.Worker:
		return errors.New("paid runs: a calibration cell is a worker")
	}
	return nil
}

// attempt is one full admission of req as reservation id: reconciliation,
// a build and an assessment, the estimate and tier, and the locked entry.
// fresh reads the quota again whatever the cache holds. It returns the
// permit, or the refusal, and the admission it tried, which a waiter checks
// its turn with.
func (g *Gate) attempt(ctx context.Context, req Request, id string, l *line, fresh bool) (Permit, Admission, *Refusal, error) {
	agent := req.Input.Agent
	family := models.Normalize(agent, req.Family)
	paid := g.Config.PaidRuns.Agent(agent)
	refuse := func(a Assessment, reason, detail string) *Refusal {
		return &Refusal{Agent: agent, Shape: req.Input.Shape, Variant: req.Input.Variant, Reason: reason, Detail: detail,
			Verdict: a.Verdict, Signals: a.Signals}
	}
	if req.Input.Variant == workerenv.Paid && !optedIn(g.Config.PaidRuns, agent) {
		return Permit{}, Admission{}, refuse(Assessment{}, ReasonNotAllowed,
			"set paid_runs."+agent+".allow with daily_cap_usd above 0"), nil
	}
	if err := g.reconcile(ctx); err != nil {
		return Permit{}, Admission{}, nil, err
	}
	var view State
	if err := g.Store.Update(ctx, func(st *State) error { view = *st; return nil }); err != nil {
		return Permit{}, Admission{}, nil, err
	}
	gone := goneWaiters(view.Tickets, l.ticket, g.probe())

	p, s, a, readErr := g.Cache.Get(ctx, g.build(ctx, req), g.Config, quota.RunEvidence{}, fresh)
	if a.Agent == "" {
		return Permit{}, Admission{}, nil, readErr
	}
	if e, ok := view.runEvidence(agent, s.ReadAt); ok {
		a = Evaluate(p, s, readErr, e, g.Config, g.now())
	}
	switch {
	case a.Verdict == Paid && p.Variant != workerenv.Paid:
		return Permit{}, Admission{}, refuse(a, ReasonFreeVariantPaid, ""), nil
	case req.Calibration && a.Verdict == Paid && !(paid.Allow && paid.Calibrate):
		return Permit{}, Admission{}, refuse(a, ReasonCalibration, "set paid_runs."+agent+".calibrate with allow"), nil
	}

	est, err := EstimateTask(g.Config, req.Records, agent, family, req.Role, req.Class)
	switch {
	case errors.Is(err, ErrUnpriced) && a.Verdict == Paid:
		r := refuse(a, ReasonUnpriced, "models."+family+" has no valid input and output price")
		r.EstimateSource = est.Source
		return Permit{}, Admission{}, r, nil
	case err != nil && !errors.Is(err, ErrUnpriced):
		return Permit{}, Admission{}, nil, err
	}
	fingerprint := Fingerprint(agent, view.AccountKeys[agent+"/"+quota.IdentityAccount], s)
	samples := slices.DeleteFunc(slices.Clone(view.Samples), func(r RateSample) bool {
		return r.Agent != agent || r.Fingerprint != fingerprint
	})
	if a.Verdict != Paid {
		costs, err := WindowCosts(s, family, est, samples)
		if err != nil {
			r := refuse(a, ReasonWindow, strings.TrimPrefix(err.Error(), "window costs: "))
			r.EstimateUSD, r.EstimateSource = est.USD, est.Source
			return Permit{}, Admission{}, r, nil
		}
		est.Windows = costs
	}
	tier := SelectTier(a, s, samples, view.Tiers[agent], g.Config)

	in := Admission{
		Reservation: Reservation{ID: id, SessionID: req.ExistingSession, Agent: agent, Role: req.Role, Class: req.Class,
			Family: family, Shape: p.Shape, Variant: p.Variant, Supervisor: req.Owner, Estimate: est,
			Session: req.Input.Shape == workerenv.Worker || req.OpenPeer, Task: true},
		Assessment: a,
		TotalLimit: g.totalLimit(),
		AgentLimit: tier.Limit,
	}
	if p.Variant == workerenv.Paid {
		in.DailyCapUSD = paid.DailyCapUSD
	}
	e, err := g.Store.enter(ctx, in, l, tier, gone, false)
	if err != nil {
		return Permit{}, Admission{}, nil, err
	}
	if e.Admitted {
		return Permit{Process: p, Assessment: a, Reservation: e.Reservation}, in, nil, nil
	}
	r := refuse(a, e.Reason, "")
	r.EstimateUSD, r.EstimateSource = est.USD, est.Source
	r.Tier, r.Live, r.Limit = tier.Tier, e.agentLive, in.AgentLimit
	r.TotalLive, r.TotalLimit = e.totalLive, in.TotalLimit
	return Permit{}, in, r, nil
}

// turn checks, on the account state alone, whether a waiter's turn came:
// the last admission it tried would now pass, or fail for a reason that no
// wait helps. Owners proven gone free their slots and tickets first.
func (g *Gate) turn(ctx context.Context, last Admission, l *line) (entry, error) {
	if err := g.reconcile(ctx); err != nil {
		return entry{}, err
	}
	var tickets []Ticket
	if err := g.Store.Update(ctx, func(st *State) error { tickets = slices.Clone(st.Tickets); return nil }); err != nil {
		return entry{}, err
	}
	return g.Store.enter(ctx, last, l, TierResult{}, goneWaiters(tickets, l.ticket, g.probe()), true)
}

// build returns the builder of req's process: the gate's config, the
// provider env_key an earlier read found, and the settings layers read
// again when the gate has a reader.
func (g *Gate) build(ctx context.Context, req Request) func() (workerenv.Process, error) {
	return func() (workerenv.Process, error) {
		in := req.Input
		in.Config = g.Config
		if in.ProviderEnvKey == "" {
			in.ProviderEnvKey = g.Cache.ProviderEnvKey(in.Agent)
		}
		if g.Layers != nil {
			layers, err := g.Layers(ctx, in)
			if err != nil {
				return workerenv.Process{}, err
			}
			in.Layers = layers
		}
		return workerenv.Build(in)
	}
}

// Finish ends the task of permit p with its measured settlement. A run that
// never started releases its reservation, uncharged; a started one, or one
// the state shows started, settles once, as Store.Settle charges it. A
// worker's session ends with its task; a pane keeps its session until
// CloseSession.
func (g *Gate) Finish(ctx context.Context, p Permit, s Settlement) error {
	s.ID, s.Agent = p.Reservation.ID, p.Reservation.Agent
	if !s.Started {
		var started bool
		err := g.Store.Update(ctx, func(st *State) error {
			r, ok := st.Reservations[s.ID]
			started = ok && (r.Started || r.Child != Owner{})
			return nil
		})
		if err != nil {
			return err
		}
		if !started {
			return g.Store.Release(ctx, s.ID)
		}
		s.Started = true
	}
	return g.Store.Settle(ctx, s)
}

// CloseSession ends peer session sessionID after its pane is confirmed
// closed: its slot frees. A task the open still holds never ran a send, so
// it is refunded; a send's task that never launched is released; one that
// did and is unsettled is charged as a run of unknown cost, at least its
// estimate when paid. A session that is not held is nothing to close.
func (g *Gate) CloseSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("paid runs: no session to close")
	}
	now := g.now()
	return g.Store.Update(ctx, func(st *State) error {
		for id, r := range st.Reservations {
			if r.Shape != workerenv.Peer || sessionKey(r) != sessionID {
				continue
			}
			if r.Task && !r.Session && (r.Started || r.Launching || r.Child != Owner{}) {
				if _, ok := st.Settled[id]; !ok {
					ended := Settlement{ID: id, Agent: r.Agent, AccountKey: r.AccountKey, Started: true, Ended: now}
					ended.ChargedUSD = charge(r, ended)
					st.Settled[id] = ended
				}
			}
			delete(st.Reservations, id)
		}
		return nil
	})
}

// runEvidence merges the run evidence of agent's settlements that ended
// after a read at readAt, which that read could not have seen: overage, fast
// mode, missing expected evidence and workspace reasons. ok is false when
// there is none.
func (st *State) runEvidence(agent string, readAt time.Time) (quota.RunEvidence, bool) {
	var e quota.RunEvidence
	found := false
	for _, s := range st.Settled {
		if s.Agent != agent || !s.Ended.After(readAt) {
			continue
		}
		x := s.Evidence
		if x.UsingOverage || x.OverageEnabled || x.Fast || x.Expected && !x.Seen || len(x.WorkspaceReasons) > 0 {
			found = true
		}
		e.UsingOverage = e.UsingOverage || x.UsingOverage
		e.OverageEnabled = e.OverageEnabled || x.OverageEnabled
		e.Fast = e.Fast || x.Fast
		if x.Expected && !x.Seen {
			e.Expected = true
		}
		e.WorkspaceReasons = append(e.WorkspaceReasons, x.WorkspaceReasons...)
	}
	slices.Sort(e.WorkspaceReasons)
	e.WorkspaceReasons = slices.Compact(e.WorkspaceReasons)
	return e, found
}

func (g *Gate) reconcile(ctx context.Context) error {
	in := g.Reconcile
	if in.Now.IsZero() {
		in.Now = g.now()
	}
	return g.Store.Reconcile(ctx, in)
}

func (g *Gate) probe() func(Owner) Presence {
	if g.Reconcile.Process != nil {
		return g.Reconcile.Process
	}
	return ProbeProcess
}

func (g *Gate) totalLimit() int {
	if n := g.Config.Limits.MaxTotalSessions; n > 0 {
		return n
	}
	return defaultTotalSessions
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Gate) wait(ctx context.Context, d time.Duration) error {
	if g.Wait != nil {
		return g.Wait(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func newReservationID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("paid runs: reservation id: %w", err)
	}
	return "r-" + hex.EncodeToString(b), nil
}

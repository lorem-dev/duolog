package paidruns

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/paidstore"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/core/record"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Reasons of a Refusal beyond those of a Decision.
const (
	ReasonUnpriced    = "no valid price"
	ReasonCalibration = "paid calibration is not allowed"
	// ReasonPaneRoute refuses a peer send whose process, built by this
	// command, is not on the route its pane was admitted with: the pane's
	// route cannot be established, so it must be opened again.
	ReasonPaneRoute = "the pane's route cannot be established"
)

// RouteKey is a digest of the route p runs on, by names only: its agent,
// shape and variant, the names in its environment, the route names it
// withholds, the matched endpoint entries, its signals, its fast-mode pin and
// its provider env_key. Unlike CacheKey it holds no config or modification
// time, so a lowered cap or a touched file does not move it.
func RouteKey(p launch.Process) string {
	names := make([]string, 0, len(p.Env))
	for _, pair := range p.Env {
		name, _, _ := strings.Cut(pair, "=")
		if !slices.Contains(workerenv.GoCacheNames, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	data, _ := json.Marshal(struct {
		Agent                   string
		Shape                   launch.Shape
		Variant                 launch.Variant
		EnvNames, Dropped, Ends []string
		Signals                 []launch.Signal
		FastPinned              bool
		ProviderEnvKey          string
	}{p.Agent, p.Shape, p.Variant, names, p.Dropped, p.EndpointEntries, p.Signals, p.FastPinned, p.ProviderEnvKey})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

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
//
// Budget, for a calibration step, is what the approved plan still allows it:
// admission refuses a task that does not fit, and a calibration step waits
// for a slot only with Budget.WaitForSession. Waiting, when set, hears true
// once the request starts to wait for a slot and false when it stops.
type Request struct {
	Input                                launch.ProcessInput
	Family, Role, Class, ExistingSession string
	Records                              []record.Delegation
	Owner                                paidrules.Owner
	Calibration, OpenPeer                bool
	Wait                                 time.Duration
	Budget                               *paidrules.StepBudget
	Waiting                              func(bool)
}

// Permit is an admitted launch: the process to run, built for this
// admission, its assessment and the reservation already in the account
// state. Before starting the child, call Store.Launch, then Store.Attach
// with the child, and end it with Finish, or CloseSession for a pane.
type Permit struct {
	Process     launch.Process
	Assessment  paidrules.Assessment
	Reservation paidrules.Reservation
	// Snapshot is the quota read the admission used. Finish learns window
	// rates from it and a read after an ordinary worker's run.
	Snapshot account.Snapshot
	// calibration marks a calibration cell, whose steps feed the rate
	// learner themselves.
	calibration bool
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
	Cache     *paidstore.Cache
	Config    *config.Config
	Reconcile ReconcileInput
	Now       func() time.Time
	Wait      func(context.Context, time.Duration) error
	Layers    func(context.Context, launch.ProcessInput) ([]launch.LayerFacts, error)
}

// Refusal says why the gate did not admit a request, with names and numbers
// only: the reason and a safe detail, the verdict and its signal names, the
// task's estimate and its source, the agent's sessions, limit and tier, the
// sessions of all agents and how long the request waited. It never holds an
// environment, settings or a route value.
type Refusal struct {
	Agent                 string
	Shape                 launch.Shape
	Variant               launch.Variant
	Reason, Detail        string
	Verdict               paidrules.Verdict
	Signals               []launch.Signal
	EstimateUSD           float64
	EstimateSource        string
	Tier                  string
	Live, Limit           int
	TotalLive, TotalLimit int
	Waited                time.Duration
	// OptedIn says the agent already has paid_runs allow with a cap, so the
	// advice to set it does not apply.
	OptedIn bool
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
	case r.Verdict == paidrules.Paid && len(r.Signals) > 0:
		names := make([]string, 0, len(r.Signals))
		for _, s := range r.Signals {
			names = append(names, s.Name+" ("+s.Layer+")")
		}
		b.WriteString("; paid: " + strings.Join(names, ", "))
	case r.Verdict == paidrules.SubscriptionOverage:
		b.WriteString("; paid overage is on; duolog stops before it")
	}
	// A window refusal is not about sessions; their counts would read like a
	// Rex window or pane limit.
	if r.Limit > 0 && r.Reason != ReasonWindow {
		fmt.Fprintf(&b, "; %s sessions %d of %d (tier %s)", r.Agent, r.Live, r.Limit, r.Tier)
		if r.Waited > 0 {
			fmt.Fprintf(&b, "; waited %d s", int(r.Waited.Round(time.Second)/time.Second))
		}
		fmt.Fprintf(&b, "; sessions %d of %d in all", r.TotalLive, r.TotalLimit)
	}
	if r.EstimateSource != "" {
		fmt.Fprintf(&b, "; estimate %.2f USD (%s)", r.EstimateUSD, r.EstimateSource)
	}
	if r.Shape == launch.Peer && r.Slot() && r.Reason != ReasonOneTask {
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
// read is paid, unless Cache.Get used a recent good read of the same route
// in place of a transient failure. A run that used overage keeps the next
// admissions at subscription_overage until a successful read made after it.
// A refusal is a *Refusal; other errors mean the state or the build failed.
func (g *Gate) Acquire(ctx context.Context, req Request) (Permit, error) {
	if err := g.check(req); err != nil {
		return Permit{}, err
	}
	wait := req.Wait
	switch {
	case req.OpenPeer:
		wait = 0
	case req.Calibration && req.Budget != nil && !req.Budget.WaitForSession:
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
	// The caller hears when the request starts to wait and when it stops.
	waiting := false
	notify := func() {
		if req.Waiting != nil && (l.ticket != "") != waiting {
			waiting = !waiting
			req.Waiting(waiting)
		}
	}
	defer func() {
		if waiting {
			req.Waiting(false)
		}
	}()
	permit, last, refusal, err := g.attempt(ctx, req, id, l, false)
	for {
		notify()
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
	case in.Shape == launch.Lead:
		return errors.New("paid runs: the lead is never admitted or launched; it is only reported")
	case in.Shape != launch.Worker && in.Shape != launch.Peer:
		return fmt.Errorf("paid runs: unknown shape %q", in.Shape)
	case in.Agent != "claude" && in.Agent != "codex":
		return fmt.Errorf("paid runs: unknown agent %q", in.Agent)
	case req.Family == "":
		return errors.New("paid runs: a request needs the model family")
	case in.Shape == launch.Worker && (req.OpenPeer || req.ExistingSession != ""):
		return errors.New("paid runs: a worker opens no pane and joins no session")
	case in.Shape == launch.Peer && req.OpenPeer == (req.ExistingSession != ""):
		return errors.New("paid runs: a peer request either opens a pane or sends to an open session")
	case req.Calibration && in.Shape != launch.Worker:
		return errors.New("paid runs: a calibration cell is a worker")
	case req.Budget != nil && !req.Calibration:
		return errors.New("paid runs: only a calibration step has a step budget")
	}
	if req.Budget != nil {
		return req.Budget.Validate()
	}
	return nil
}

// attempt is one full admission of req as reservation id: reconciliation,
// a build and an assessment, the estimate and tier, and the locked entry.
// fresh reads the quota again whatever the cache holds. It returns the
// permit, or the refusal, and the admission it tried, which a waiter checks
// its turn with. An account that changed between the read and the entry is
// read again once, so its rates and tier are its own; a second change is
// refused.
func (g *Gate) attempt(ctx context.Context, req Request, id string, l *line, fresh bool) (Permit, Admission, *Refusal, error) {
	permit, in, refusal, err := g.attemptOnce(ctx, req, id, l, fresh)
	if err == nil && refusal != nil && refusal.Reason == ReasonAccountChanged {
		return g.attemptOnce(ctx, req, id, l, true)
	}
	return permit, in, refusal, err
}

func (g *Gate) attemptOnce(ctx context.Context, req Request, id string, l *line, fresh bool) (Permit, Admission, *Refusal, error) {
	agent := req.Input.Agent
	family := route.Normalize(agent, req.Family)
	paid := g.Config.PaidRuns.Agent(agent)
	refuse := func(a paidrules.Assessment, reason, detail string) *Refusal {
		return &Refusal{Agent: agent, Shape: req.Input.Shape, Variant: req.Input.Variant, Reason: reason, Detail: detail,
			Verdict: a.Verdict, Signals: a.Signals, OptedIn: paidrules.OptedIn(g.Config.PaidRuns, agent)}
	}
	if req.Input.Variant == launch.Paid && !paidrules.OptedIn(g.Config.PaidRuns, agent) {
		return Permit{}, Admission{}, refuse(paidrules.Assessment{}, ReasonNotAllowed,
			"set paid_runs."+agent+".allow with daily_cap_usd above 0"), nil
	}
	if err := g.reconcile(ctx); err != nil {
		return Permit{}, Admission{}, nil, err
	}
	p, s, a, readErr := g.Cache.Get(ctx, g.build(ctx, req), g.Config, account.RunEvidence{}, fresh)
	if a.Agent == "" {
		return Permit{}, Admission{}, nil, readErr
	}
	// The state after the read: an identity the read found has already
	// dropped the old account's rates and tier.
	var view State
	if err := g.Store.Update(ctx, func(st *State) error { view = *st; return nil }); err != nil {
		return Permit{}, Admission{}, nil, err
	}
	gone := goneWaiters(view.Tickets, l.ticket, g.probe())
	accountKey := view.AccountKeys[agent+"/"+quota.IdentityAccount]
	if e, ok := view.runEvidence(agent, s.ReadAt); ok {
		a = paidrules.Evaluate(p, s, readErr, e, g.Config, g.now())
	}
	if req.ExistingSession != "" {
		// A send runs in its pane, on the route and billing class the pane
		// was admitted with, whatever this command's environment says.
		if open, ok := view.openSession(agent, req.ExistingSession); ok {
			if open.Route == "" || open.Route != RouteKey(p) || open.Workspace && a.Account != "workspace" {
				return Permit{}, Admission{}, refuse(a, ReasonPaneRoute, "close the pane and open it again"), nil
			}
			if open.Paid && a.Verdict != paidrules.Paid {
				a.Verdict = paidrules.Paid
				a.Signals = append(a.Signals, launch.Signal{Name: "pane:paid", Layer: "pane admission"})
			}
		}
	}
	switch {
	case a.Verdict == paidrules.Paid && p.Variant != launch.Paid:
		return Permit{}, Admission{}, refuse(a, ReasonFreeVariantPaid, ""), nil
	case req.Calibration && a.Verdict == paidrules.Paid && (!paid.Allow || !paid.Calibrate):
		return Permit{}, Admission{}, refuse(a, ReasonCalibration, "set paid_runs."+agent+".calibrate with allow"), nil
	}

	est, err := paidrules.EstimateTask(g.Config, req.Records, agent, family, req.Role, req.Class)
	switch {
	case errors.Is(err, paidrules.ErrUnpriced) && a.Verdict == paidrules.Paid:
		r := refuse(a, ReasonUnpriced, "models."+family+" has no valid input and output price")
		r.EstimateSource = est.Source
		return Permit{}, Admission{}, r, nil
	case err != nil && !errors.Is(err, paidrules.ErrUnpriced):
		return Permit{}, Admission{}, nil, err
	}
	fingerprint := paidrules.Fingerprint(agent, accountKey, s)
	samples := slices.DeleteFunc(slices.Clone(view.Samples), func(r paidrules.RateSample) bool {
		return r.Agent != agent || r.Fingerprint != fingerprint
	})
	if a.Verdict != paidrules.Paid {
		costs, err := paidrules.WindowCosts(s, family, est, samples)
		if err != nil {
			r := refuse(a, ReasonWindow, strings.TrimPrefix(err.Error(), "window costs: "))
			r.EstimateUSD, r.EstimateSource = est.USD, est.Source
			return Permit{}, Admission{}, r, nil
		}
		est.Windows = costs
	}
	// What the approved plan still allows the step, checked in the same
	// admission as the account's cap and slots.
	step := ""
	if b := req.Budget; b != nil {
		if reason := b.Check(est, a.Verdict == paidrules.Paid); reason != "" {
			r := refuse(a, paidrules.ReasonStepBudget, reason)
			r.EstimateUSD, r.EstimateSource = est.USD, est.Source
			return Permit{}, Admission{}, r, nil
		}
		step = b.Key()
	}
	tier := paidrules.SelectTier(a, s, samples, view.Tiers[agent], g.Config)

	in := Admission{
		Reservation: paidrules.Reservation{ID: id, SessionID: req.ExistingSession, Agent: agent, Role: req.Role, Class: req.Class,
			Family: family, Shape: p.Shape, Variant: p.Variant, Supervisor: req.Owner, Estimate: est,
			Session: req.Input.Shape == launch.Worker || req.OpenPeer, Task: true, Step: step, Route: RouteKey(p),
			Peer: peerID(req.Input)},
		Assessment: a,
		TotalLimit: g.totalLimit(),
		AgentLimit: tier.Limit,
		ReadAt:     s.ReadAt,
		accountKey: &accountKey,
	}
	if p.Variant == launch.Paid {
		in.DailyCapUSD = paid.DailyCapUSD
	}
	e, err := g.Store.enter(ctx, in, l, tier, gone, false)
	if err != nil {
		return Permit{}, Admission{}, nil, err
	}
	if e.Admitted {
		return Permit{Process: p, Assessment: a, Reservation: e.Reservation, Snapshot: s,
			calibration: req.Calibration}, in, nil, nil
	}
	r := refuse(a, e.Reason, windowDetail(e.Reason, agent, est, a))
	r.EstimateUSD, r.EstimateSource = est.USD, est.Source
	r.Tier, r.Live, r.Limit = tier.Tier, e.agentLive, in.AgentLimit
	r.TotalLive, r.TotalLimit = e.totalLive, in.TotalLimit
	return Permit{}, in, r, nil
}

// windowDetail names the window that left no headroom when reason is
// ReasonWindow, and is empty for any other reason.
func windowDetail(reason, agent string, est paidrules.Estimate, a paidrules.Assessment) string {
	if reason != ReasonWindow {
		return ""
	}
	return paidrules.WindowShortfall(agent, est.Windows, a.UsableShare)
}

// turn checks, on the account state alone, whether a waiter's turn came:
// the last admission it tried would now pass, or fail for a reason that no
// wait helps. Owners proven gone free their slots and tickets first.
func (g *Gate) turn(ctx context.Context, last Admission, l *line) (entry, error) {
	if err := g.reconcile(ctx); err != nil {
		return entry{}, err
	}
	var tickets []paidrules.Ticket
	if err := g.Store.Update(ctx, func(st *State) error { tickets = slices.Clone(st.Tickets); return nil }); err != nil {
		return entry{}, err
	}
	return g.Store.enter(ctx, last, l, paidrules.TierResult{}, goneWaiters(tickets, l.ticket, g.probe()), true)
}

// build returns the builder of req's process: the gate's config, the
// provider env_key an earlier read found, and the settings layers read
// again when the gate has a reader. The layers are found with the
// environment the process gets, not duolog's: a CLAUDE_CONFIG_DIR or
// CODEX_HOME that env_passthrough does not pass moves no layer.
func (g *Gate) build(ctx context.Context, req Request) func() (launch.Process, error) {
	return func() (launch.Process, error) {
		in := req.Input
		in.Config = g.Config
		if in.ProviderEnvKey == "" {
			in.ProviderEnvKey = g.Cache.ProviderEnvKey(in.Agent)
		}
		if in.Plan == "" {
			in.Plan = g.Cache.Plan(in.Agent)
		}
		if g.Layers != nil {
			own, err := workerenv.Build(in)
			if err != nil {
				return launch.Process{}, err
			}
			in.Locations.Environ = own.Env
			// The home whose settings the agent loads is the process's own,
			// when its environment names one, such as a HOME in paid env.
			if home, err := trusthome.HomeIn(own.Env); err == nil {
				in.Locations.Home = home
			}
			layers, err := g.Layers(ctx, in)
			if err != nil {
				return launch.Process{}, err
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
// CloseSession. After an ordinary worker's run, Finish learns window rates
// from it, as learn says.
func (g *Gate) Finish(ctx context.Context, p Permit, s paidrules.Settlement) error {
	if err := g.settle(ctx, p, s); err != nil {
		return err
	}
	g.learn(ctx, p, s)
	return nil
}

// learnTimeout bounds the quota read after a run that learn makes.
const learnTimeout = 20 * time.Second

// learn feeds the rate learner one ordinary worker run: the movement of the
// windows between its admission's read and a read made now, over the run's
// tokens, in real units. Only a started run with complete usage, of a
// verdict that is not paid and on the account it was admitted for, counts;
// a pane, a calibration cell, whose steps observe themselves, and a run
// whose context ended learn nothing. Nor does a run that ends while the read
// of its process is younger than the quota age limit: its result does not
// wait for a read, and so short an interval rarely moves a window enough
// to count. The read runs no model and spends no tokens, and goes to the
// cache, so the next admission uses it. A failed read or observation learns
// nothing, which leaves the rates and the tier as they are.
func (g *Gate) learn(ctx context.Context, p Permit, s paidrules.Settlement) {
	r := p.Reservation
	if p.calibration || r.Shape != launch.Worker || p.Assessment.Verdict == paidrules.Paid || !s.Started || !s.CompleteUsage ||
		p.Snapshot.ReadAt.IsZero() || r.Family == "" || g.Cache == nil || g.Cache.Read == nil || ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), learnTimeout)
	defer cancel()
	after, fresh, err := g.Cache.After(ctx, p.Process, g.Config)
	if err != nil || fresh {
		return
	}
	key := ""
	if g.Store.View(ctx, func(st State) error {
		key = st.AccountKeys[r.Agent+"/"+quota.IdentityAccount]
		return nil
	}) != nil || key != r.AccountKey {
		return
	}
	_ = g.Store.Observe(ctx, g.Config, paidrules.Interval{Agent: r.Agent, Family: r.Family,
		Fingerprint: paidrules.Fingerprint(r.Agent, key, after), Before: p.Snapshot, After: after,
		Input: s.Input, Cached: s.Cached, Output: s.Output})
}

// settle is Finish's settlement.
func (g *Gate) settle(ctx context.Context, p Permit, s paidrules.Settlement) error {
	s.ID, s.Agent = p.Reservation.ID, p.Reservation.Agent
	if !s.Started {
		var started bool
		err := g.Store.Update(ctx, func(st *State) error {
			r, ok := st.Reservations[s.ID]
			started = ok && (r.Started || r.Child != paidrules.Owner{})
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
			if r.Shape != launch.Peer || sessionKey(r) != sessionID {
				continue
			}
			if r.Task && !r.Session && (r.Started || r.Launching || r.Child != paidrules.Owner{}) {
				if _, ok := st.Settled[id]; !ok {
					ended := paidrules.Settlement{ID: id, Agent: r.Agent, AccountKey: r.AccountKey, Started: true, Ended: now,
						Windows: r.Estimate.Windows}
					ended.ChargedUSD = charge(r, ended)
					st.Settled[id] = ended
				}
			}
			delete(st.Reservations, id)
		}
		return nil
	})
}

// openSession returns the reservation that holds agent's session id.
func (st *State) openSession(agent, id string) (paidrules.Reservation, bool) {
	for _, r := range st.Reservations {
		if r.Session && r.Agent == agent && sessionKey(r) == id {
			return r, true
		}
	}
	return paidrules.Reservation{}, false
}

// runEvidence merges the run evidence of agent's settlements that ended
// after a read at readAt, which that read could not have seen: overage, fast
// mode, missing expected evidence and workspace reasons. ok is false when
// there is none.
func (st *State) runEvidence(agent string, readAt time.Time) (account.RunEvidence, bool) {
	var e account.RunEvidence
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

func (g *Gate) probe() func(paidrules.Owner) Presence {
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

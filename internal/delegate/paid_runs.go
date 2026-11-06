package delegate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/usage"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// NewGate opens the account admission gate of one delegation: the user's
// account state, reads of the quota each built process sees, and the
// settings layers each build reads. Tests of packages that run real
// delegations with fake agent binaries replace it, so that no quota read
// starts a binary and no test touches the user's account state.
var NewGate = defaultGate

func defaultGate(cfg *config.Config) (*paidruns.Gate, error) {
	st, err := paidruns.OpenUserState("")
	if err != nil {
		return nil, err
	}
	reader := quota.Reader{IdentitySink: func(agent, kind string, raw []byte) error {
		_, err := st.Identity(agent, kind, raw)
		return err
	}}
	return &paidruns.Gate{Store: st, Cache: &paidruns.Cache{Read: reader.ReadProcess}, Config: cfg, Layers: readLayers}, nil
}

// readLayers reads the agent's settings layers for one build, every time, so
// a build never uses facts older than its admission.
func readLayers(_ context.Context, in workerenv.ProcessInput) ([]workerenv.LayerFacts, error) {
	var endpoints map[string]string
	if in.Agent == "claude" && in.Config != nil {
		endpoints = in.Config.PaidRuns.Claude.SubscriptionEndpoints
	}
	var facts []workerenv.LayerFacts
	for _, layer := range workerenv.SettingsLayers(in.Agent, in.Locations) {
		facts = append(facts, workerenv.ReadLayer(in.Agent, layer, endpoints, os.ReadFile, os.Stat))
	}
	return facts, nil
}

// workerEnv builds a worker's process: its environment, its argv prefix and
// its settings, from one launch description. The login check, the quota read
// and the run all use what it builds; nothing else makes a worker's
// environment.
func workerEnv(in workerenv.ProcessInput) (workerenv.Process, error) {
	in.Shape = workerenv.Worker
	return workerenv.Build(in)
}

// workerInput describes the worker launch of route in variant: duolog's own
// environment, the route's context, the protected roots and the extra
// variables, the run id and bridged servers' env, that pass Build's route
// detection.
func workerInput(cfg *config.Config, route router.Route, variant workerenv.Variant, leadID, role, root string,
	roots, extra []string) workerenv.ProcessInput {
	home, _ := os.UserHomeDir()
	environ := os.Environ()
	return workerenv.ProcessInput{Agent: route.Agent, Shape: workerenv.Worker, Variant: variant, Config: cfg,
		Locations: workerenv.Locations{Home: home, Root: root, GOOS: runtime.GOOS, Environ: environ},
		Environ:   environ, LeadID: leadID, Role: role, StateDir: project.StateRoot(root),
		Minimal: route.Context == "minimal", ProtectedRoots: roots, ExtraEnv: extra}
}

// variants are the variants delegation tries, in order: every free
// candidate of the role before any paid one, and a paid pass only when the
// role's candidates, by paidruns.Candidates, hold a paid variant. paid names
// the agents that have one.
func variants(cfg *config.Config, role string, opts Options) ([]workerenv.Variant, map[string]bool) {
	var targets []config.Target
	if opts.Entry != nil {
		targets = []config.Target{*opts.Entry}
	} else if spec, ok := cfg.Roles[role]; ok {
		targets = []config.Target{spec.Use, spec.Fallback}
		// A role's derived fallback runs the other agent.
		for _, agent := range []string{"claude", "codex"} {
			targets = append(targets, config.Target{Agent: agent})
		}
	}
	paid := map[string]bool{}
	for _, c := range paidruns.Candidates(targets, opts.Entry != nil, cfg.PaidRuns) {
		if c.Variant == workerenv.Paid {
			paid[c.Target.Agent] = true
		}
	}
	if len(paid) == 0 {
		return []workerenv.Variant{workerenv.Free}, paid
	}
	return []workerenv.Variant{workerenv.Free, workerenv.Paid}, paid
}

// permitRun is one actual worker invocation behind the gate: its permit, and
// whether its child started.
type permitRun struct {
	gate    *paidruns.Gate
	permit  paidruns.Permit
	started bool
}

// admit acquires a permit for a worker of route described by in. A refusal
// comes back as such, with no error.
func admit(ctx context.Context, g *paidruns.Gate, cfg *config.Config, in workerenv.ProcessInput, route router.Route,
	role, class string, records []journal.Delegation, opts Options) (*permitRun, *paidruns.Refusal, error) {
	req := paidruns.Request{Input: in, Family: models.Normalize(route.Agent, route.Model), Role: role, Class: class,
		Records: records, Owner: paidruns.ProcessOwner(os.Getpid()), Calibration: opts.Calibration,
		Wait: paidruns.SessionWait(cfg)}
	permit, err := g.Acquire(ctx, req)
	var refusal *paidruns.Refusal
	if errors.As(err, &refusal) {
		return nil, refusal, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &permitRun{gate: g, permit: permit}, nil, nil
}

// launch marks the start before the child exists and gives the runner the
// hook that records the child before the run goes on. A child whose record
// fails is stopped by the runner and still counts as started, so its hold
// is never freed.
func (a *permitRun) launch(ctx context.Context, spec *runner.Spec) error {
	if err := a.gate.Store.Launch(ctx, a.permit.Reservation.ID); err != nil {
		return err
	}
	spec.Process = &a.permit.Process
	spec.Started = func(pid int) error {
		a.started = true
		// The child exists: it is recorded even when the delegation is being
		// cancelled.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
		defer cancel()
		return a.gate.Store.Attach(ctx, a.permit.Reservation.ID, paidruns.ProcessOwner(pid))
	}
	return nil
}

// release gives back a permit whose child never started, uncharged.
func (a *permitRun) release(ctx context.Context) error {
	return a.finish(ctx, nil, router.Route{Target: config.Target{Agent: a.permit.Reservation.Agent}}, runner.Result{})
}

// sameRoute makes sure the login was checked on the admitted process. The
// gate builds it again, so a settings edit or a newly found provider key in
// between moves the route; the moved route then gets its own check, and a
// failed check gives the permit back.
func (a *permitRun) sameRoute(ctx context.Context, checked workerenv.Process, cacheDir string) error {
	if a.permit.Process.CacheKey == checked.CacheKey {
		return nil
	}
	if p := newChecker(cacheDir, a.permit.Process).Agent(ctx, a.permit.Process.Agent); p != nil {
		return errors.Join(fmt.Errorf("%s: %s, after the worker's route changed during admission; delegate again", p.Subject(), p.Reason),
			a.release(ctx))
	}
	return nil
}

// settleTimeout bounds the settlement of a run that ended, also after the
// delegation was cancelled.
const settleTimeout = 10 * time.Second

// finish settles the attempt once with what the run measured: its tokens,
// its priced and reported cost and its billing evidence. A run whose child
// never started releases its hold uncharged. It writes with a context that
// the delegation's cancellation does not end.
func (a *permitRun) finish(ctx context.Context, cfg *config.Config, route router.Route, r runner.Result) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	s := paidruns.Settlement{Input: r.Usage.Input, Cached: r.Usage.Cached, Output: r.Usage.Output,
		ReportedUSD: r.ReportedUSD, Evidence: r.Billing, Started: a.started, CompleteUsage: a.started && r.UsageKnown}
	if cfg != nil {
		if priced, ok := usage.Cost(cfg, route.Agent, route.Model, s.Input, s.Cached, s.Output); ok && priced >= 0 {
			s.PricedUSD = priced
		}
	}
	if err := a.gate.Finish(ctx, a.permit, s); err != nil {
		return fmt.Errorf("paid runs: settle the %s run: %w", route.Agent, err)
	}
	return nil
}

// RefusalLine says why the gate refused a candidate, for the lead. Peer
// commands word their refusals with it too.
func RefusalLine(r *paidruns.Refusal) string {
	switch r.Reason {
	case paidruns.ReasonFreeVariantPaid, paidruns.ReasonNotAllowed:
		var signals []string
		for _, s := range r.Signals {
			signals = append(signals, s.Name+" in "+s.Layer)
		}
		line := r.Agent + " worker is paid"
		if len(signals) > 0 {
			line += " (" + strings.Join(slices.Compact(signals), ", ") + ")"
		}
		return line + "; set paid_runs." + r.Agent + " to allow"
	case paidruns.ReasonAgentSessions, paidruns.ReasonOneTask, paidruns.ReasonQueue:
		line := fmt.Sprintf("%s sessions %d of %d (tier %s)", r.Agent, r.Live, r.Limit, r.Tier)
		if r.Reason == paidruns.ReasonOneTask {
			line = r.Agent + " runs one task at a time"
		}
		if r.Waited > 0 {
			line += fmt.Sprintf("; waited %d s", int(r.Waited.Round(time.Second)/time.Second))
		}
		return line
	case paidruns.ReasonTotalSessions:
		return fmt.Sprintf("sessions %d of %d in all", r.TotalLive, r.TotalLimit)
	case paidruns.ReasonCap:
		return fmt.Sprintf("%s daily cap reached; this task is estimated at %.2f USD (%s)", r.Agent, r.EstimateUSD, r.EstimateSource)
	}
	return r.Error()
}

// leadLine sends the task to the lead with the reasons every candidate was
// refused.
func leadLine(role string, refusals []string, p Placement) string {
	return oneLine(role + " -> lead: " + strings.Join(refusals, "; ") + "; " + p.String())
}

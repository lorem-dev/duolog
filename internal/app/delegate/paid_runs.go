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

	"github.com/lorem-dev/duolog/internal/app/launch"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/core/usage"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/project"
)

// workerEnv builds a worker's process: its environment, its argv prefix and
// its settings, from one launch description. The login check, the quota read
// and the run all use what it builds; nothing else makes a worker's
// environment.
func workerEnv(build launch.Build, in corelaunch.ProcessInput) (corelaunch.Process, error) {
	in.Shape = corelaunch.Worker
	return build(in)
}

// workerInput describes the worker launch of route in variant: duolog's own
// environment, the route's context, the protected roots and the extra
// variables, the run id and bridged servers' env, that pass Build's route
// detection.
func workerInput(cfg *config.Config, route coreroute.Route, variant corelaunch.Variant, leadID, role, root string,
	roots, extra, needs []string) corelaunch.ProcessInput {
	home, _ := os.UserHomeDir()
	environ := os.Environ()
	return corelaunch.ProcessInput{Agent: route.Agent, Shape: corelaunch.Worker, Variant: variant, Config: cfg,
		Locations: corelaunch.Locations{Home: home, Root: root, GOOS: runtime.GOOS, Environ: environ},
		Environ:   environ, LeadID: leadID, Role: role, StateDir: project.StateRoot(root),
		Minimal: route.Context == "minimal", ProtectedRoots: roots, ExtraEnv: extra, PluginNeeds: pluginNeeds(needs)}
}

// pluginNeeds are the plugin names among a task's "kind:name" needs; a Claude
// worker keeps these plugins on.
func pluginNeeds(needs []string) []string {
	var names []string
	for _, need := range needs {
		if kind, name, err := caps.ParseNeed(need); err == nil && kind == caps.KindPlugin {
			names = append(names, name)
		}
	}
	return names
}

// variants are the variants delegation tries, in order: every free
// candidate of the role before any paid one, and a paid pass only when the
// role's candidates, by paidrules.Candidates, hold a paid variant of an agent
// with a Quota. paid names the agents that have one.
func variants(d Deps, cfg *config.Config, role string, opts Options) ([]corelaunch.Variant, map[string]bool) {
	var targets []config.Target
	if opts.Entry != nil {
		targets = []config.Target{*opts.Entry}
	} else if spec, ok := cfg.Roles[role]; ok {
		targets = []config.Target{spec.Use, spec.Fallback}
		// A role's derived fallback runs the other agent.
		for _, agent := range d.Names {
			targets = append(targets, config.Target{Agent: agent})
		}
	}
	paid := map[string]bool{}
	for _, c := range paidrules.Candidates(targets, opts.Entry != nil, cfg.PaidRuns) {
		// An agent without a quota reader is never read, so its spend could
		// not be judged: it gets no paid pass.
		if c.Variant == corelaunch.Paid && d.Agents[c.Target.Agent].Quota != nil {
			paid[c.Target.Agent] = true
		}
	}
	if len(paid) == 0 {
		return []corelaunch.Variant{corelaunch.Free}, paid
	}
	return []corelaunch.Variant{corelaunch.Free, corelaunch.Paid}, paid
}

// permitRun is one actual worker invocation behind the gate: its permit, and
// whether its child started.
type permitRun struct {
	gate       *paidruns.Gate
	permit     paidruns.Permit
	started    bool
	attached   bool
	chargedUSD float64
}

// admit acquires a permit for a worker of route described by in. A refusal
// comes back as such, with no error.
func admit(ctx context.Context, g *paidruns.Gate, cfg *config.Config, in corelaunch.ProcessInput, route coreroute.Route,
	role, class string, records []journal.Delegation, opts Options) (*permitRun, *paidruns.Refusal, error) {
	clear := opts.admissionWait("waiting for " + route.Agent + " quota")
	defer clear()
	waiting := func(on bool) {
		if opts.Waiting != nil {
			opts.Waiting(on)
		}
		if on {
			opts.admissionWait("waiting for a " + route.Agent + " session slot")
		}
	}
	req := paidruns.Request{Input: in, Family: coreroute.Normalize(route.Agent, route.Model), Role: role, Class: class,
		Records: records, Owner: paidruns.ProcessOwner(os.Getpid()), Calibration: opts.Calibration,
		Wait: paidruns.SessionWait(cfg), Budget: opts.Budget, Waiting: waiting}
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
func (a *permitRun) launch(ctx context.Context, spec *run.Spec) error {
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
		err := a.gate.Store.Attach(ctx, a.permit.Reservation.ID, paidruns.ProcessOwner(pid))
		a.attached = err == nil
		return err
	}
	return nil
}

// release gives back a permit whose child never started, uncharged.
func (a *permitRun) release(ctx context.Context) error {
	return a.finish(ctx, nil, coreroute.Route{Target: config.Target{Agent: a.permit.Reservation.Agent}}, run.Result{})
}

// sameRoute makes sure the login was checked on the admitted process. The
// gate builds it again, so a settings edit or a newly found provider key in
// between moves the route; the moved route then gets its own check, and a
// failed check gives the permit back.
func (a *permitRun) sameRoute(ctx context.Context, d Deps, checked corelaunch.Process, cacheDir string) error {
	if a.permit.Process.CacheKey == checked.CacheKey {
		return nil
	}
	if p := d.checker(cacheDir, a.permit.Process).Agent(ctx, a.permit.Process.Agent); p != nil {
		return errors.Join(fmt.Errorf("%s: %s, after the worker's route changed during admission; delegate again", p.Subject(), p.Reason),
			a.release(ctx))
	}
	return nil
}

// settleTimeout bounds the settlement of a run that ended, also after the
// delegation was cancelled.
const settleTimeout = 10 * time.Second

// finish settles the attempt once with what the run measured: its tokens,
// its priced and reported cost and its billing evidence. Usage is complete
// only when the vendor reported the whole run's; a run cut off mid-request
// counts at least its estimate when paid. A run whose child never started
// releases its hold uncharged. It writes with a context that the
// delegation's cancellation does not end.
func (a *permitRun) finish(ctx context.Context, cfg *config.Config, route coreroute.Route, r run.Result) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	s := paidrules.Settlement{Input: r.Usage.Input, Cached: r.Usage.Cached, Output: r.Usage.Output,
		ReportedUSD: r.ReportedUSD, Evidence: r.Billing, Started: a.started, CompleteUsage: a.started && r.UsageComplete}
	s.ID, s.Agent, s.Ended = a.permit.Reservation.ID, route.Agent, time.Now()
	if cfg != nil {
		// The run's own usage, so Claude's cache writes cost their premium.
		if priced, ok := usage.Cost(cfg, route.Agent, route.Model, r.Usage); ok && priced >= 0 {
			s.PricedUSD = priced
		}
	}
	if err := a.gate.Finish(ctx, a.permit, s); err != nil {
		if a.attached {
			if saveErr := a.gate.Store.DeferSettlement(s); saveErr != nil {
				err = errors.Join(err, fmt.Errorf("save settlement for retry: %w", saveErr))
			} else {
				err = fmt.Errorf("%w; settlement saved for the next admission", err)
			}
		}
		return fmt.Errorf("paid runs: settle the %s run: %w", route.Agent, err)
	}

	return a.gate.Store.View(ctx, func(st paidruns.State) error {
		a.chargedUSD = st.Settled[a.permit.Reservation.ID].ChargedUSD
		return nil
	})
}

// RefusalLine says why the gate refused a candidate, for the lead. Peer
// commands word their refusals with it too, so it names the shape. The
// advice to allow paid runs goes only to an agent that has not opted in.
func RefusalLine(r *paidruns.Refusal) string {
	switch r.Reason {
	case paidruns.ReasonFreeVariantPaid, paidruns.ReasonNotAllowed:
		return paidRefusalLine(r)
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

// paidRefusalLine preserves the gate's safe detail in the lead's paid refusal.
func paidRefusalLine(r *paidruns.Refusal) string {
	var signals []string
	for _, s := range r.Signals {
		signals = append(signals, s.Name+" in "+s.Layer)
	}
	shape := string(r.Shape)
	if shape == "" {
		shape = string(corelaunch.Worker)
	}
	line := r.Agent + " " + shape + " is paid"
	if len(signals) > 0 {
		line += " (" + strings.Join(slices.Compact(signals), ", ") + ")"
	}
	// A not-allowed refusal's detail repeats the paid_runs advice below.
	if r.Reason == paidruns.ReasonFreeVariantPaid && r.Detail != "" {
		line += "; " + r.Detail
	}
	if r.OptedIn {
		return line
	}
	return line + "; set paid_runs." + r.Agent + " to allow"
}

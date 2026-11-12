package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/launch"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// Usage is what a probe call reported: tokens, the CLI's own cost when it gave
// one, and whether it reported usage at all.
type Usage = usage.Probed

// Probe is what doctor needs of one agent to check it: which model its probe
// calls, the probe call itself, and the agent's own metadata commands
// (--version, the model catalog).
type Probe interface {
	// Model returns the model the probe calls, from the models the agent
	// lists (visible; empty for an agent with no catalog).
	Model(cfg *config.Config, visible []route.Entry) (string, error)
	// Probe makes the one model call in dir, as the process p the gate built.
	// It starts the agent, calls started with the pid before it waits (an
	// error from started stops the call and is returned), and checks the
	// answer. The usage it measured comes back also with an error.
	Probe(ctx context.Context, cfg *config.Config, p corelaunch.Process, dir, model string, started func(pid int) error) (Usage, error)
	// Metadata runs the agent with args and returns what it printed.
	Metadata(ctx context.Context, args ...string) ([]byte, error)
}

// probeSettleTimeout bounds the settlement of a probe that ended, also after
// doctor was cancelled.
const probeSettleTimeout = 10 * time.Second

// probe makes one model call, the empty-call form from the live verification
// bench, with read-only access and an explicit output file for Codex. It is a
// worker run like any other: the account gate admits it in the free worker's
// process, or in the paid one only when paid runs are allowed and the cap
// leaves room, and it never waits for a slot. A refused probe makes no model
// call. The probe runs in the process the gate built, its environment and
// settings included, and settles once with the usage and cost it reported.
func probe(ctx context.Context, d Deps, agent string, cfg *config.Config, entries []catalogEntry) (err error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	pr, ok := d.Probes[agent]
	if !ok {
		return fmt.Errorf("no probe for %s", agent)
	}
	dir, err := os.MkdirTemp("", "duolog-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	list := make([]route.Entry, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry.Entry)
	}
	model, err := pr.Model(cfg, list)
	if err != nil {
		return err
	}
	g, err := d.PaidGate(cfg)
	if err != nil {
		return err
	}
	permit, err := admitProbe(ctx, d.Build, g, cfg, agent, route.Normalize(agent, model), dir)
	if err != nil {
		return err
	}
	started := false
	var measured Usage
	defer func() {
		// One settlement of what the call measured, also when it failed or
		// was cancelled; a call that never started gives its hold back.
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeSettleTimeout)
		defer cancel()
		s := paidrules.Settlement{Input: measured.Input, Cached: measured.Cached, Output: measured.Output,
			ReportedUSD: measured.CostUSD, Started: started, CompleteUsage: started && measured.Known}
		if priced, ok := usage.Cost(cfg, agent, model, measured.Tokens); ok && priced >= 0 {
			s.PricedUSD = priced
		}
		err = errors.Join(err, g.Finish(settleCtx, permit, s))
	}()
	if err := g.Store.Launch(ctx, permit.Reservation.ID); err != nil {
		return err
	}
	// The agent's Probe starts the process and calls attach with its pid: a
	// call that cannot be recorded does not run on unseen.
	attach := func(pid int) error {
		started = true
		attachCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeSettleTimeout)
		defer cancel()
		return g.Store.Attach(attachCtx, permit.Reservation.ID, paidruns.ProcessOwner(pid))
	}
	measured, err = pr.Probe(ctx, cfg, permit.Process, dir, model, attach)
	return err
}

// admitProbe admits the probe as a worker of agent that runs in dir: the
// free variant, then the paid one when paid runs allow it. Its settings
// layers are found where the probe looks: the home folder, dir, this platform
// and the probe's own environment. It never waits; a refusal is the error,
// worded as delegate words it, and nothing is run.
func admitProbe(ctx context.Context, build launch.Build, g *paidruns.Gate, cfg *config.Config, agent, family, dir string) (paidruns.Permit, error) {
	home, _ := os.UserHomeDir()
	free, err := build(corelaunch.ProcessInput{Agent: agent, Shape: corelaunch.Worker, Variant: corelaunch.Free,
		Config: cfg, Environ: os.Environ(), Role: "probe", Minimal: true})
	if err != nil {
		return paidruns.Permit{}, err
	}
	loc := corelaunch.Locations{Home: home, Root: dir, GOOS: runtime.GOOS, Environ: free.Env}
	variants := []corelaunch.Variant{corelaunch.Free}
	for _, c := range paidrules.Candidates([]config.Target{{Agent: agent}}, true, cfg.PaidRuns) {
		if c.Variant == corelaunch.Paid {
			variants = append(variants, corelaunch.Paid)
		}
	}
	var refusals []string
	for _, variant := range variants {
		in := corelaunch.ProcessInput{Agent: agent, Shape: corelaunch.Worker, Variant: variant, Config: cfg,
			Environ: os.Environ(), Role: "probe", Minimal: true, Locations: loc}
		permit, err := g.Acquire(ctx, paidruns.Request{Input: in, Family: family, Role: "probe",
			Owner: paidruns.ProcessOwner(os.Getpid())})
		var refusal *paidruns.Refusal
		if errors.As(err, &refusal) {
			refusals = append(refusals, delegate.RefusalLine(refusal))
			continue
		}
		return permit, err
	}
	return paidruns.Permit{}, fmt.Errorf("refused, no model call made: %s", strings.Join(refusals, "; "))
}

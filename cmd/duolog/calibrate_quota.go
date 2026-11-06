package main

import (
	"context"
	"errors"
	"maps"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/lorem-dev/duolog/internal/calibration"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// quotaReader replaces calibration's quota reads in tests; nil reads them
// through the account gate. This file is the only one of calibration that
// imports internal/quota.
var quotaReader calibration.QuotaReader

// Window lengths in minutes that internal/quota reports.
const (
	fiveHourMinutes = 300
	weeklyMinutes   = 10080
)

// calibrationAccounts is calibration's view of the user's accounts: one
// account gate, opened on first use with the person's config, whose
// route-aware cache holds every quota read of the command.
type calibrationAccounts struct {
	cfg  *config.Config
	root string

	once sync.Once
	gate *paidruns.Gate
	err  error

	// latest is each agent's last successful read, base the read the next
	// observation measures from, and observedAt the read the last one ended at.
	mu         sync.Mutex
	latest     map[string]quota.Snapshot
	base       map[string]quota.Snapshot
	observedAt map[string]time.Time
}

func newCalibrationAccounts(cfg *config.Config, root string) *calibrationAccounts {
	return &calibrationAccounts{cfg: cfg, root: root}
}

func (a *calibrationAccounts) open() (*paidruns.Gate, error) {
	a.once.Do(func() { a.gate, a.err = delegate.NewGate(a.cfg) })
	return a.gate, a.err
}

// worker is the free worker process of agent that calibration's cells run
// as, with the person's config, so proxy and CA variables reach its reads.
func (a *calibrationAccounts) worker(agent string) workerenv.ProcessInput {
	home, _ := os.UserHomeDir()
	environ := os.Environ()
	return workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker, Variant: workerenv.Free, Config: a.cfg,
		Environ: environ, Locations: workerenv.Locations{Home: home, Root: a.root, GOOS: runtime.GOOS, Environ: environ}}
}

// Quota is calibration's quota source: the test reader when one is set, else
// the gate's cache, which alone decides how old a reading may be. A rejected
// reading or a reached spend control is an error, so the run never spends
// blind.
func (a *calibrationAccounts) Quota() *calibration.QuotaCache {
	if quotaReader != nil {
		return &calibration.QuotaCache{Read: quotaReader}
	}
	return &calibration.QuotaCache{Shared: func(ctx context.Context, agent string, fresh bool) ([]calibration.Window, error) {
		g, err := a.open()
		if err != nil {
			return nil, err
		}
		s, _, err := g.Read(ctx, a.worker(agent), fresh)
		if err != nil {
			return nil, err
		}
		a.remember(agent, s)
		if s.Rejected || s.SpendControlReached {
			return nil, errors.New("quota rejected or spend control reached")
		}
		return quotaWindows(s), nil
	}}
}

// remember keeps a successful read: the latest, and the first as the base of
// the next observation.
func (a *calibrationAccounts) remember(agent string, s quota.Snapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.latest == nil {
		a.latest, a.base, a.observedAt = map[string]quota.Snapshot{}, map[string]quota.Snapshot{}, map[string]time.Time{}
	}
	a.latest[agent] = s
	if _, ok := a.base[agent]; !ok {
		a.base[agent] = s
	}
}

// Reconcile ends the account reservations whose owners are proven gone,
// before a resume runs anything again; an uncertain one stays held.
func (a *calibrationAccounts) Reconcile(ctx context.Context) error {
	g, err := a.open()
	if err != nil {
		return err
	}
	in := g.Reconcile
	in.Now = time.Now()
	return g.Store.Reconcile(ctx, in)
}

// Held reports whether a step receipt still holds an account reservation.
func (a *calibrationAccounts) Held(ctx context.Context, receipt string) (bool, error) {
	g, err := a.open()
	if err != nil {
		return false, err
	}
	return g.Store.StepHeld(ctx, receipt)
}

// Observe feeds the shared rate learner one finished step: the movement
// between the read before it and the latest read, over the step's tokens,
// in real units. Without a read after the base there is nothing to measure,
// and the same reads are never observed twice.
func (a *calibrationAccounts) Observe(ctx context.Context, agent, family string, input, cached, output int64) error {
	a.mu.Lock()
	before, after := a.base[agent], a.latest[agent]
	a.mu.Unlock()
	if after.ReadAt.IsZero() || !after.ReadAt.After(before.ReadAt) {
		return nil
	}
	g, err := a.open()
	if err != nil {
		return err
	}
	var key string
	if err := g.Store.View(ctx, func(st paidruns.State) error {
		key = st.AccountKeys[agent+"/"+quota.IdentityAccount]
		return nil
	}); err != nil {
		return err
	}
	in := paidruns.Interval{Agent: agent, Family: family, Fingerprint: paidruns.Fingerprint(agent, key, after),
		Before: before, After: after, Input: input, Cached: cached, Output: output}
	if err := g.Store.Observe(ctx, a.cfg, in); err != nil {
		return err
	}
	a.mu.Lock()
	a.base[agent], a.observedAt[agent] = after, after.ReadAt
	a.mu.Unlock()
	return nil
}

// Budgets reports each agent's billing through the gate, from the same reads
// as Quota: the verdict of its worker route, the account and its usable
// share, its paid spend and tasks in flight, and its paid_runs opt-ins. A
// failed read is paid, never a cheaper verdict.
func (a *calibrationAccounts) Budgets(ctx context.Context) (map[string]calibration.BillingBudget, error) {
	g, err := a.open()
	if err != nil {
		return nil, err
	}
	budgets := map[string]calibration.BillingBudget{}
	for _, agent := range []string{"claude", "codex"} {
		r, err := g.Report(ctx, a.worker(agent))
		if r.Assessment.Agent == "" {
			return nil, err
		}
		paid := a.cfg.PaidRuns.Agent(agent)
		b := calibration.BillingBudget{Verdict: string(r.Assessment.Verdict), Account: r.Assessment.Account,
			UsableShare: r.Assessment.UsableShare, SpentUSD: r.SpentUSD, InflightUSD: r.InflightUSD,
			Allow: paid.Allow && paid.DailyCapUSD > 0, Calibrate: paid.Calibrate}
		if b.Allow {
			b.CapUSD = paid.DailyCapUSD
		}
		budgets[agent] = b
	}
	return budgets, nil
}

// quotaWindows maps the account's windows, then each model family's own
// limits (Codex's per-model limits) with the family in Model, sorted by
// family; admission keeps the reserve in the ones of the cell's model too.
// A window the account refuses counts as used up. Percentages are taken as
// the agent reports them, in points of the whole window, once.
func quotaWindows(s quota.Snapshot) []calibration.Window {
	var ws []calibration.Window
	add := func(model string, windows []quota.Window) {
		for _, w := range windows {
			kind := ""
			switch w.DurationMinutes {
			case fiveHourMinutes:
				kind = calibration.KindFiveHour
			case weeklyMinutes:
				kind = calibration.KindWeekly
			default:
				continue
			}
			used := w.UsedPercent
			if w.Rejected() {
				used = 100
			}
			ws = append(ws, calibration.Window{Kind: kind, Model: model, UsedPercent: used, ResetsAt: w.ResetsAt})
		}
	}
	add("", s.Windows)
	for _, family := range slices.Sorted(maps.Keys(s.ModelLimits)) {
		add(family, s.ModelLimits[family])
	}
	return ws
}

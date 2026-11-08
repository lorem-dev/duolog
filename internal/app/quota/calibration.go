package quota

import (
	"context"
	"errors"
	"maps"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/budget"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

// Window lengths in minutes that the quota reads report.
const (
	fiveHourMinutes = 300
	weeklyMinutes   = 10080
	// individualKind is the window kind of a Codex individual limit.
	individualKind = "individual"
)

// Accounts is calibration's view of the user's accounts: one account gate,
// opened on first use with the person's config, whose route-aware cache
// holds every quota read of the command.
type Accounts struct {
	open        func() (*paidruns.Gate, error)
	calibration budget.QuotaReader
	cfg         *config.Config
	root        string

	once sync.Once
	gate *paidruns.Gate
	err  error

	// latest is each agent's last successful read, base the read the next
	// observation measures from, and observedAt the read the last one ended at.
	mu         sync.Mutex
	latest     map[string]account.Snapshot
	base       map[string]account.Snapshot
	observedAt map[string]time.Time
}

// NewAccounts is the accounts of a calibration command in root, with cfg:
// open opens the account gate, once; d.Calibration replaces the reads.
func NewAccounts(d Deps, open func() (*paidruns.Gate, error), cfg *config.Config, root string) *Accounts {
	return &Accounts{open: open, calibration: d.Calibration, cfg: cfg, root: root}
}

// Gate is the account gate, opened on first use.
func (a *Accounts) Gate() (*paidruns.Gate, error) {
	a.once.Do(func() { a.gate, a.err = a.open() })
	return a.gate, a.err
}

// ObservedAt is the read the last observation of agent ended at, zero when
// none was observed.
func (a *Accounts) ObservedAt(agent string) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.observedAt[agent]
}

// worker is the free worker process of agent that calibration's cells run
// as, with the person's config, so proxy and CA variables reach its reads.
func (a *Accounts) worker(agent string) corelaunch.ProcessInput {
	home, _ := os.UserHomeDir()
	environ := os.Environ()
	return corelaunch.ProcessInput{Agent: agent, Shape: corelaunch.Worker, Variant: corelaunch.Free, Config: a.cfg,
		Environ: environ, Locations: corelaunch.Locations{Home: home, Root: a.root, GOOS: runtime.GOOS, Environ: environ}}
}

// Quota is calibration's quota source: the test reader when one is set, else
// the gate's cache, which alone decides how old a reading may be. A rejected
// reading or a reached spend control is an error, so the run never spends
// blind.
func (a *Accounts) Quota() *budget.QuotaCache {
	if a.calibration != nil {
		return &budget.QuotaCache{Read: a.calibration}
	}
	return &budget.QuotaCache{Shared: func(ctx context.Context, agent string, fresh bool) ([]budget.Window, error) {
		g, err := a.Gate()
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
		return Windows(s), nil
	}}
}

// remember keeps a successful read: the latest, and the first as the base of
// the next observation.
func (a *Accounts) remember(agent string, s account.Snapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.latest == nil {
		a.latest, a.base, a.observedAt = map[string]account.Snapshot{}, map[string]account.Snapshot{}, map[string]time.Time{}
	}
	a.latest[agent] = s
	if _, ok := a.base[agent]; !ok {
		a.base[agent] = s
	}
}

// Reconcile ends the account reservations whose owners are proven gone,
// before a resume runs anything again; an uncertain one stays held.
func (a *Accounts) Reconcile(ctx context.Context) error {
	g, err := a.Gate()
	if err != nil {
		return err
	}
	in := g.Reconcile
	in.Now = time.Now()
	return g.Store.Reconcile(ctx, in)
}

// Held reports whether a step receipt still holds an account reservation.
func (a *Accounts) Held(ctx context.Context, receipt string) (bool, error) {
	g, err := a.Gate()
	if err != nil {
		return false, err
	}
	return g.Store.StepHeld(ctx, receipt)
}

// Observe feeds the shared rate learner one finished step: the movement
// between the read before it and the latest read, over the step's tokens,
// in real units. Without a read after the base there is nothing to measure,
// and the same reads are never observed twice.
func (a *Accounts) Observe(ctx context.Context, agent, family string, input, cached, output int64) error {
	a.mu.Lock()
	before, after := a.base[agent], a.latest[agent]
	a.mu.Unlock()
	if after.ReadAt.IsZero() || !after.ReadAt.After(before.ReadAt) {
		return nil
	}
	g, err := a.Gate()
	if err != nil {
		return err
	}
	var key string
	if err := g.Store.View(ctx, func(st paidruns.State) error {
		key = st.AccountKeys[agent+"/"+account.IdentityAccount]
		return nil
	}); err != nil {
		return err
	}
	in := paidrules.Interval{Agent: agent, Family: family, Fingerprint: paidrules.Fingerprint(agent, key, after),
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
// failed read is paid, never a cheaper verdict. A switched-off agent is not
// read and has no budget: it runs no step, which delegate refuses.
func (a *Accounts) Budgets(ctx context.Context) (map[string]budget.BillingBudget, error) {
	g, err := a.Gate()
	if err != nil {
		return nil, err
	}
	budgets := map[string]budget.BillingBudget{}
	for _, agent := range []string{"claude", "codex"} {
		if a.cfg.AgentSwitchedOff(agent) {
			continue
		}
		r, err := g.Report(ctx, a.worker(agent))
		if r.Assessment.Agent == "" {
			return nil, err
		}
		paid := a.cfg.PaidRuns.Agent(agent)
		b := budget.BillingBudget{Verdict: string(r.Assessment.Verdict), Account: r.Assessment.Account,
			UsableShare: r.Assessment.UsableShare, SpentUSD: r.SpentUSD, InflightUSD: r.InflightUSD,
			Allow: paid.Allow && paid.DailyCapUSD > 0, Calibrate: paid.Calibrate}
		if b.Allow {
			b.CapUSD = paid.DailyCapUSD
		}
		budgets[agent] = b
	}
	return budgets, nil
}

// Windows maps the account's windows, then each model family's own limits
// (Codex's per-model limits) with the family in Model, sorted by family;
// admission keeps the reserve in the ones of the cell's model too. A window
// the account refuses counts as used up. Percentages are taken as the agent
// reports them, in points of the whole window, once.
//
// A Codex individual limit, the per-person spend limit of an enterprise or
// workspace seat, has no length and is the account's long limit: it maps to
// the weekly kind, so it gets the weekly lead reserve and cap of the usable
// share, and calibration can run on an account that has no other window.
// Next to a weekly window of the same scope it is left out, since budget
// keeps one window of each kind; paid runs admission still holds every
// step's worker to it.
func Windows(s account.Snapshot) []budget.Window {
	var ws []budget.Window
	add := func(model string, windows []account.Window) {
		weekly := slices.ContainsFunc(windows, func(w account.Window) bool { return w.DurationMinutes == weeklyMinutes })
		for _, w := range windows {
			var kind string
			switch {
			case w.DurationMinutes == fiveHourMinutes:
				kind = budget.KindFiveHour
			case w.DurationMinutes == weeklyMinutes, w.Kind == individualKind && !weekly:
				kind = budget.KindWeekly
			default:
				continue
			}
			used := w.UsedPercent
			if w.Rejected() {
				used = 100
			}
			ws = append(ws, budget.Window{Kind: kind, Model: model, UsedPercent: used, ResetsAt: w.ResetsAt})
		}
	}
	add("", s.Windows)
	for _, family := range slices.Sorted(maps.Keys(s.ModelLimits)) {
		add(family, s.ModelLimits[family])
	}
	return ws
}

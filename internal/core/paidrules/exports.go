package paidrules

import (
	"slices"
	"time"

	coreaccount "github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// The names below are for internal/app/paidruns and internal/adapter/paidstore,
// which keep the I/O of paid runs and the code that changes their state. Each
// forwards to the helper the rules use, so the rules read as they did before
// the split.

// PaceAgents are the agents whose pace is read.
func PaceAgents() []string { return slices.Clone(paceAgents) }

// QuotaMaxAge is balance.max_age_seconds: the oldest quota read a verdict or
// the cache trusts. A config without it uses the default.
func QuotaMaxAge(cfg *config.Config) time.Duration { return quotaMaxAge(cfg) }

// TierSettings are cfg's session tiers and balance settings, each invalid or
// missing value replaced by its default.
func TierSettings(cfg *config.Config) (config.SessionTiers, config.Balance) {
	return tierSettings(cfg)
}

// OptedIn reports whether agent allows paid runs with a cap to spend.
func OptedIn(paid config.PaidRuns, agent string) bool { return optedIn(paid, agent) }

// WindowScope is the model family an account window limits, "" for all.
func WindowScope(w coreaccount.Window, known map[string]config.Model) string {
	return windowScope(w, known)
}

// WindowShape lists the read's windows by scope, kind, group, model and
// length, in a stable order.
func WindowShape(s coreaccount.Snapshot) []string { return windowShape(s) }

// WindowTerms are a window's reserve and unmeasured bound.
func WindowTerms(w coreaccount.Window) (reserve, bound float64) { return windowTerms(w) }

// Finite reports whether no value is NaN or infinite.
func Finite(values ...float64) bool { return finite(values...) }

const (
	// FitSlack absorbs float rounding at an exact ceiling.
	FitSlack = fitSlack
	// MinMeasurements is the fewest measured intervals whose costs replace
	// the bound.
	MinMeasurements = minMeasurements
	// USDEpsilon absorbs float rounding, so a request that lands exactly on
	// the cap is admitted.
	USDEpsilon = usdEpsilon
)

// Validate refuses a budget that names no step or holds an amount that is
// not a finite number in range.
func (b *StepBudget) Validate() error { return b.validate() }

// Check reports why a task of estimate est does not fit the budget, "" when
// it does.
func (b *StepBudget) Check(est Estimate, paid bool) string { return b.check(est, paid) }

// Validate refuses an estimate with invalid tokens or amounts.
func (e Estimate) Validate() error { return e.validate() }

// Validate refuses a settlement with no id or invalid tokens or costs.
func (in Settlement) Validate() error { return in.validate() }

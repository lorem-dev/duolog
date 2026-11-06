package paidruns

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/quota"
)

// Window terms as raw fractions. The lead reserves stay as they are and apply
// to the usable part of a window; the bounds are a task's cost in a window
// without measurements, as calibration starts.
const (
	reserveWeekly   = 0.15
	reserveFiveHour = 0.10
	boundWeekly     = 0.01
	boundFiveHour   = 0.05
	weeklyMinutes   = 10080
	// minMeasurements is the fewest measured intervals whose costs replace
	// the bound.
	minMeasurements = 3
	// fitSlack absorbs float rounding at an exact ceiling.
	fitSlack = 1e-9
)

// WindowCosts places a task of the model family in every window of s that
// limits it, as raw fractions of the whole window: the account's windows,
// the family's own and those that name no known family. Another family's
// limit, even a rejected one, does not apply. Reserved is left at zero for
// the caller's tasks in flight.
//
// A rejected window, or one that reset before the read, counts as full. A
// read without a window that resets after it, an account rejection or spend
// control reached, and NaN, infinite or negative values are errors, so
// missing evidence never reads as headroom.
//
// The cost in a window comes from samples of this agent's account in real
// units: each measures a window's movement per weighted token, matched by
// family and window kind and scope. With fewer than three measured intervals
// the cost is the bound, raised by any measurement above it; from three on it
// is the pooled rate, never below the p90 of the measurements. USD plays no
// part, so the usable share never scales money.
func WindowCosts(s quota.Snapshot, family string, estimate Estimate,
	samples []RateSample) ([]WindowCost, error) {
	e := estimate
	if e.Input < 0 || e.Cached < 0 || e.Output < 0 || e.Cached > e.Input || !finite(e.USD) || e.USD < 0 {
		return nil, errors.New("window costs: invalid estimate")
	}
	switch {
	case s.Rejected:
		return nil, errors.New("window costs: the account is rejected")
	case s.SpendControlReached:
		return nil, errors.New("window costs: the account is at its spend control")
	case s.ReadAt.IsZero():
		return nil, errors.New("window costs: no quota read")
	}
	for _, r := range samples {
		if !finite(r.Movement, r.WeightedTokens) || r.Movement < 0 || r.WeightedTokens < 0 || r.Intervals < 0 {
			return nil, fmt.Errorf("window costs: invalid %s rate sample", r.Family)
		}
	}
	defaults, err := config.DefaultModels()
	if err != nil {
		return nil, err
	}
	weighted := float64(e.Input) + float64(e.Output)*outputWeight(defaults)

	type scoped struct {
		w     quota.Window
		scope string
	}
	var ws []scoped
	for _, w := range s.Windows {
		ws = append(ws, scoped{w, windowScope(w, defaults)})
	}
	for _, scope := range slices.Sorted(maps.Keys(s.ModelLimits)) {
		for _, w := range s.ModelLimits[scope] {
			ws = append(ws, scoped{w, scope})
		}
	}
	var costs []WindowCost
	current := false
	for _, x := range ws {
		w := x.w
		if !finite(w.UsedPercent) || w.UsedPercent < 0 || w.UsedPercent > 100 {
			return nil, errors.New("window costs: invalid window")
		}
		if family != "" && x.scope != "" && x.scope != family && defaults[x.scope].Agent != "" {
			continue
		}
		used := w.UsedPercent / 100
		if w.Rejected() || !w.ResetsAt.IsZero() && !w.ResetsAt.After(s.ReadAt) {
			used = 1
		}
		current = current || w.ResetsAt.After(s.ReadAt)
		key := WindowKey{Kind: w.Kind, Family: x.scope, Reset: w.ResetsAt}
		reserve, bound := windowTerms(w)
		costs = append(costs, WindowCost{Key: key, Used: used, Reserve: reserve,
			Cost: measuredCost(samples, family, key, weighted, bound)})
	}
	if !current {
		return nil, errors.New("window costs: no window resets after the read")
	}
	return costs, nil
}

// FitsWindows reports whether a task fits every window on an account of
// usable share u: used/u + reserved/u + cost/u + reserve <= 1 in each. No
// windows, an invalid u or an invalid value do not fit.
func FitsWindows(costs []WindowCost, usableShare float64) bool {
	u := usableShare
	if len(costs) == 0 || !finite(u) || u <= 0 || u > 1 {
		return false
	}
	for _, c := range costs {
		if !finite(c.Used, c.Reserved, c.Cost, c.Reserve) || min(c.Used, c.Reserved, c.Cost, c.Reserve) < 0 {
			return false
		}
		if c.Used/u+c.Reserved/u+c.Cost/u+c.Reserve > 1+fitSlack {
			return false
		}
	}
	return true
}

// windowTerms are a window's reserve and unmeasured bound: weekly terms for a
// window of a week or longer, five-hour terms for a shorter one, and the
// larger of each for a window of unknown length.
func windowTerms(w quota.Window) (reserve, bound float64) {
	switch {
	case w.DurationMinutes >= weeklyMinutes:
		return reserveWeekly, boundWeekly
	case w.DurationMinutes > 0:
		return reserveFiveHour, boundFiveHour
	}
	return reserveWeekly, boundFiveHour
}

// windowScope is the model family an account window limits, "" for all. A
// window scoped to a model name that is not a known family keeps that name,
// so it limits every family.
func windowScope(w quota.Window, known map[string]config.Model) string {
	switch w.Kind {
	case "weekly_opus":
		return "opus"
	case "weekly_sonnet":
		return "sonnet"
	}
	for _, word := range strings.Fields(w.Model) {
		if known[word].Agent != "" {
			return word
		}
	}
	return w.Model
}

// outputWeight is the largest output-to-input price ratio of the default
// models: the weight of an output token in the rates' weighted tokens. Cached
// input counts at full input weight. Both keep the task's weight at or above
// what the rate learner counts with those prices.
func outputWeight(defaults map[string]config.Model) float64 {
	weight := 1.0
	for _, p := range defaults {
		if p.Input > 0 {
			weight = max(weight, p.Output/p.Input)
		}
	}
	return weight
}

// measuredCost is a task's cost of weighted tokens in the window key, from
// the samples of family that measured it.
func measuredCost(samples []RateSample, family string, key WindowKey, weighted, bound float64) float64 {
	var costs []float64
	var movement, tokens float64
	intervals := 0
	for _, r := range samples {
		if r.Family != family || r.Window.Kind != key.Kind || r.Window.Family != key.Family ||
			r.Intervals == 0 || r.WeightedTokens == 0 || r.Movement == 0 {
			continue
		}
		costs = append(costs, r.Movement/r.WeightedTokens*weighted)
		movement += r.Movement
		tokens += r.WeightedTokens
		intervals += r.Intervals
	}
	if len(costs) == 0 {
		return bound
	}
	slices.Sort(costs)
	if intervals < minMeasurements {
		return max(bound, costs[len(costs)-1])
	}
	// Nearest rank: the smallest measurement at or above 90% of them.
	p90 := costs[int(math.Ceil(0.9*float64(len(costs))))-1]
	return max(movement/tokens*weighted, p90)
}

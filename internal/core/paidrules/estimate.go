package paidrules

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/core/record"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// The cold estimate of a target without history: calibration's high bound
// for its largest scope, priced as input and output with no cache discount.
const (
	coldTokens     = 1500000
	coldInputShare = 0.85
	coldSource     = "no history: bound of 1.5M tokens, 85% input, 15% output"
)

// ErrUnpriced means the target has no valid price. EstimateTask still returns
// the tokens, which window math may use, but no money: a paid admission must
// refuse rather than count the run as free.
var ErrUnpriced = errors.New("no valid price")

// EstimateTask estimates one task of role and class on the agent's model
// family from the journal: the nearest-rank 90th percentile of the priced
// cost of earlier runs on the same agent and family. Runs of the same class
// count; without a class, runs of the same role. A failed or interrupted run
// counts once it used tokens; profile reruns of another delegation do not.
// Without such runs the estimate is the cold bound, which Source names.
//
// Input includes the cached subset, so cached tokens are priced once. An
// invalid record of the target is an error. A missing or invalid price
// returns the tokens with ErrUnpriced, never a zero cost.
func EstimateTask(cfg *config.Config, records []record.Delegation,
	agent, family, role, class string) (Estimate, error) {
	if cfg == nil {
		return Estimate{}, errors.New("estimate: no config")
	}
	byClass := class != "" && class != learn.Unclassified
	var runs []record.Delegation
	for _, d := range records {
		if d.Agent != agent || route.Normalize(d.Agent, d.Model) != family {
			continue
		}
		if d.Input < 0 || d.Cached < 0 || d.Output < 0 || d.Cached > d.Input {
			return Estimate{}, fmt.Errorf("estimate: %s %s run %q has invalid token counts", agent, family, d.ID)
		}
		if d.ProfileOf != "" || d.Input+d.Output == 0 {
			continue
		}
		if byClass && d.Class == class || !byClass && d.Role == role {
			runs = append(runs, d)
		}
	}

	priceErr := checkPrice(cfg, agent, family)
	cost := func(t usage.Tokens) float64 {
		if priceErr != nil {
			return 0
		}
		usd, _ := usage.Cost(cfg, agent, family, t)
		return usd
	}
	// A run's tokens with its Claude cache writes, which cost more than input.
	// Counts that do not fit together are priced without the writes.
	tokens := func(d record.Delegation) usage.Tokens {
		t := usage.Tokens{Input: d.Input, Cached: d.Cached, Write: d.CacheWrite, WriteHour: d.CacheWriteHour, Output: d.Output}
		if !t.Valid() {
			t.Write, t.WriteHour = 0, 0
		}
		return t
	}
	var e Estimate
	var measured usage.Tokens
	if len(runs) == 0 {
		input := int64(math.Round(coldTokens * coldInputShare))
		e = Estimate{Input: input, Output: coldTokens - input, Source: coldSource}
		measured = usage.Tokens{Input: e.Input, Output: e.Output}
	} else {
		// Prices are the family's, so ranking by cost ranks by weighted
		// tokens; unpriced runs rank by their tokens.
		slices.SortStableFunc(runs, func(a, b record.Delegation) int {
			return cmp.Or(cmp.Compare(cost(tokens(a)), cost(tokens(b))),
				cmp.Compare(a.Input+a.Output, b.Input+b.Output))
		})
		d := runs[int(math.Ceil(0.9*float64(len(runs))))-1]
		scope := "class " + class
		if !byClass {
			scope = "role " + role
		}
		e = Estimate{Input: d.Input, Cached: d.Cached, Output: d.Output,
			Source: fmt.Sprintf("journal p90 of %d runs of %s %s, %s", len(runs), agent, family, scope)}
		measured = tokens(d)
	}
	if priceErr != nil {
		return e, fmt.Errorf("%w for %s model %s (estimate: %s); paid runs refuse it: %w",
			ErrUnpriced, agent, family, e.Source, priceErr)
	}
	e.USD = cost(measured)
	return e, nil
}

// checkPrice checks the configured price of the family for agent. Every rate
// must be finite and not negative, and input and output above zero, so that
// no task is ever priced as free.
func checkPrice(cfg *config.Config, agent, family string) error {
	p, ok := cfg.Models[route.Normalize(agent, family)]
	switch {
	case !ok || p.Agent != agent:
		return errors.New("not in models")
	case !finite(p.Input, p.Cached, p.Output) || p.Input < 0 || p.Cached < 0 || p.Output < 0:
		return errors.New("a price is not a number at or above zero")
	case p.Input == 0 || p.Output == 0:
		return errors.New("input or output price is zero")
	}
	return nil
}

func finite(values ...float64) bool {
	return !slices.ContainsFunc(values, func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) })
}

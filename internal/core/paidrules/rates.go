package paidrules

import (
	"errors"
	"fmt"

	coreaccount "github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
)

// Interval is what duolog's runs of one agent and model family used between
// two quota reads of the agent: Input, Cached and Output tokens, Cached a
// subset of Input, complete for every run that ended in between. Fingerprint
// is Fingerprint of the account at the After read.
type Interval struct {
	Agent, Family, Fingerprint string
	Before, After              coreaccount.Snapshot
	Input, Cached, Output      int64
}

// LearnedRate is the learned rate of one family in one window: PerToken is
// the highest rate of its qualified intervals, as a fraction of the whole
// window per weighted token. Measured is false until Intervals reaches three;
// before that the rate is unmeasured and qualifies nothing.
type LearnedRate struct {
	Window    WindowKey
	PerToken  float64
	Intervals int
	Measured  bool
}

// WeightedTokens weighs tokens of agent's model family by the configured
// prices, relative to uncached input: input + cached*(cached/input price - 1)
// + output*output/input price. Cached is a subset of input. Negative tokens,
// a cached count above input and a family without a valid price are errors;
// a token is never weighed as free because its price is missing.
func WeightedTokens(cfg *config.Config, agent, family string,
	input, cached, output int64) (float64, error) {
	if cfg == nil {
		return 0, errors.New("weighted tokens: no config")
	}
	if input < 0 || cached < 0 || output < 0 || cached > input {
		return 0, fmt.Errorf("weighted tokens: invalid %s %s token counts", agent, family)
	}
	if err := checkPrice(cfg, agent, family); err != nil {
		return 0, fmt.Errorf("weighted tokens: %w for %s model %s: %w", ErrUnpriced, agent, family, err)
	}
	p := cfg.Models[route.Normalize(agent, family)]
	w := float64(input) + float64(cached)*(p.Cached/p.Input-1) + float64(output)*p.Output/p.Input
	if !finite(w) || w < 0 {
		return 0, fmt.Errorf("weighted tokens: invalid %s %s weight", agent, family)
	}
	return w, nil
}

// Learned is the rate of agent's family in window, matched by kind and
// scope, from samples of fingerprint. Samples of another fingerprint and
// samples without one, such as rates from before account fingerprints, never
// count, so they qualify no tier.
func Learned(samples []RateSample, agent, family, fingerprint string, window WindowKey) LearnedRate {
	r := LearnedRate{Window: window}
	if fingerprint == "" {
		return r
	}
	for _, x := range samples {
		if x.Agent != agent || x.Family != family || x.Fingerprint != fingerprint ||
			x.Window.Kind != window.Kind || x.Window.Family != window.Family ||
			x.Intervals <= 0 || !finite(x.Movement, x.WeightedTokens) || x.Movement < 0 || x.WeightedTokens <= 0 {
			continue
		}
		r.Intervals += x.Intervals
		r.PerToken = max(r.PerToken, x.Movement/x.WeightedTokens)
		r.Window.Reset = x.Window.Reset
	}
	r.Measured = r.Intervals >= minMeasurements
	return r
}

package paidruns

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
)

// Session tiers, from the fewest live sessions to the most.
const (
	TierSmall  = "small"
	TierMedium = "medium"
	TierLarge  = "large"
)

var tierOrder = []string{TierSmall, TierMedium, TierLarge}

// tierCuts are a window's cut points in share of the window per million
// weighted tokens: a rate above small is the small tier, one at or below
// large is the large tier, and one between is medium. Each cut sits at the
// geometric middle of two tiers' reference rates.
type tierCuts struct{ small, large float64 }

var (
	fiveHourCuts = tierCuts{small: .065, large: .015}
	weeklyCuts   = tierCuts{small: .013, large: .003}
)

// cutSlack absorbs float rounding of a rate that sits exactly at a cut.
const cutSlack = 1e-9

// quotaMaxAge is balance.max_age_seconds: the oldest quota read a verdict or
// the cache trusts. A config without it uses the default.
func quotaMaxAge(cfg *config.Config) time.Duration {
	seconds := config.DefaultBalance().MaxAgeSeconds
	if cfg != nil && cfg.Balance.MaxAgeSeconds > 0 {
		seconds = cfg.Balance.MaxAgeSeconds
	}
	return time.Duration(seconds) * time.Second
}

// SelectTier is the session tier of agent a from its quota read s and its
// learned rate samples, with the tier it had before.
//
// Every window of s that limits the whole account votes; a window of one
// model family does not, and a window the read does not show abstains. A
// window's rate is the highest rate of a model family of the agent that
// rests on at least three intervals, each of balance.min_interval_share
// movement or more; a window without one votes small. A five-hour window
// votes with the five-hour cuts, a window of a week or longer with the weekly
// cuts, and one of unknown length with the stricter weekly ones; a rate at a
// cut gets the larger tier. The tier is the smallest vote.
//
// A step down applies at once. A step up needs every window's rate at or
// below the crossed cut scaled by 1 - balance.margin; otherwise the tier
// rises only as far as all windows allow that way, and never falls below
// the previous tier for it. An empty or unknown previous tier is small, so
// a fingerprint change or a drift reset, which drops the samples and the
// tier, starts at small again.
//
// A paid verdict, a workspace account, an unknown agent or a read without
// an account window is small whatever the rates say. subscription_overage
// keeps the tier its rates give. The reason names the deciding window and
// its rate in share per million weighted tokens, or the cause of small.
// Rates holds each measured account window's rate in share per million.
// Limit is the tier's configured session count; Live is kept from previous,
// since live sessions are counted by the caller.
func SelectTier(a Assessment, snapshot quota.Snapshot, samples []RateSample,
	previous TierResult, cfg *config.Config) TierResult {
	tiers, balance := tierSettings(cfg)
	result := TierResult{Live: previous.Live}
	votes := windowVotes(a.Agent, snapshot, samples, balance)
	for _, v := range votes {
		if v.measured {
			if result.Rates == nil {
				result.Rates = map[string]float64{}
			}
			result.Rates[v.kind] = max(result.Rates[v.kind], v.rate)
		}
	}
	finish := func(tier, reason string) TierResult {
		result.Tier, result.Reason, result.Limit = tier, reason, tiers.Limit(tier)
		return result
	}
	forced := func(cause string) TierResult {
		if rest := describeVotes(votes, -1); rest != "" {
			cause += "; " + rest
		}
		return finish(TierSmall, cause)
	}
	switch {
	case a.Agent != "claude" && a.Agent != "codex":
		return finish(TierSmall, "unknown agent")
	case a.Verdict != Subscription && a.Verdict != SubscriptionOverage:
		return forced("paid verdict")
	case a.Account != "personal":
		return forced("workspace account")
	case len(votes) == 0:
		return finish(TierSmall, "no account window in the quota read")
	}

	// plain is the smallest vote at the cuts; stepped the smallest at the
	// cuts scaled by the margin, which a step up must meet.
	plain, stepped := 0, 0
	for i, v := range votes {
		if v.plain < votes[plain].plain {
			plain = i
		}
		if v.stepped < votes[stepped].stepped {
			stepped = i
		}
	}
	before := max(slices.Index(tierOrder, previous.Tier), 0)
	tier, decider := votes[plain].plain, plain
	if tier > before {
		if held := max(before, votes[stepped].stepped); held < tier {
			tier, decider = held, stepped
		}
	}

	v := votes[decider]
	var head, rest string
	switch {
	case !v.measured:
		var unmeasured, measured []string
		for _, u := range votes {
			if u.measured {
				measured = append(measured, u.describe())
			} else {
				unmeasured = append(unmeasured, u.kind)
			}
		}
		head, rest = "unmeasured "+strings.Join(unmeasured, ", "), strings.Join(measured, ", ")
	case tier < v.plain:
		head = v.describe() + "; a step up to " + tierOrder[tier+1] + " needs " +
			formatRate(v.cuts.at(tier)*(1-balance.Margin)) + " share/M or less"
	case tier == 0:
		head = v.describe() + " above " + formatRate(v.cuts.small)
	default:
		head = v.describe() + " at or below " + formatRate(v.cuts.at(tier-1))
	}
	if v.measured {
		rest = describeVotes(votes, decider)
	}
	if rest != "" {
		head += "; " + rest
	}
	return finish(tierOrder[tier], head)
}

// windowVote is one account window's rate and its votes, as indexes of
// tierOrder: plain at the cuts, stepped at the cuts scaled by the margin.
type windowVote struct {
	kind           string
	rate           float64
	measured       bool
	cuts           tierCuts
	plain, stepped int
}

func (v windowVote) describe() string {
	if !v.measured {
		return v.kind + " unmeasured"
	}
	return v.kind + " " + formatRate(v.rate) + " share/M"
}

// at is the cut between tier index i and the next larger tier.
func (c tierCuts) at(i int) float64 {
	if i == 0 {
		return c.small
	}
	return c.large
}

// windowVotes are the votes of s's account windows, in the read's order.
func windowVotes(agent string, s quota.Snapshot, samples []RateSample, balance config.Balance) []windowVote {
	known, err := config.DefaultModels()
	if err != nil {
		known = nil
	}
	var votes []windowVote
	for _, w := range s.Windows {
		if windowScope(w, known) != "" {
			continue
		}
		v := windowVote{kind: w.Kind, cuts: weeklyCuts}
		if w.DurationMinutes > 0 && w.DurationMinutes < weeklyMinutes {
			v.cuts = fiveHourCuts
		}
		v.rate, v.measured = windowRate(agent, w.Kind, samples, balance.MinIntervalShare)
		if v.measured {
			v.plain = vote(v.rate, v.cuts, 1)
			v.stepped = vote(v.rate, v.cuts, 1-balance.Margin)
		}
		votes = append(votes, v)
	}
	return votes
}

// vote is the tier index of a rate at cuts scaled by scale.
func vote(rate float64, c tierCuts, scale float64) int {
	switch {
	case rate <= c.large*scale*(1+cutSlack):
		return 2
	case rate <= c.small*scale*(1+cutSlack):
		return 1
	}
	return 0
}

// windowRate is the highest qualified family rate of agent in the account
// window kind, in share per million weighted tokens. A family qualifies with
// three intervals or more whose movement is at least minShare each. Its rate
// is the pooled rate, never below the p90 of its samples, as admission
// prices a task. An invalid sample makes the window unmeasured.
func windowRate(agent, kind string, samples []RateSample, minShare float64) (float64, bool) {
	type family struct {
		movement, tokens float64
		intervals        int
		rates            []float64
	}
	families := map[string]*family{}
	for _, r := range samples {
		if r.Agent != agent || r.Window.Kind != kind || r.Window.Family != "" {
			continue
		}
		if !finite(r.Movement, r.WeightedTokens) || r.Movement < 0 || r.WeightedTokens < 0 || r.Intervals < 0 {
			return 0, false
		}
		if r.Intervals == 0 || r.WeightedTokens == 0 {
			continue
		}
		f := families[r.Family]
		if f == nil {
			f = &family{}
			families[r.Family] = f
		}
		f.movement += r.Movement
		f.tokens += r.WeightedTokens
		f.intervals += r.Intervals
		f.rates = append(f.rates, r.Movement/r.WeightedTokens)
	}
	best, measured := 0.0, false
	for _, f := range families {
		if f.intervals < minMeasurements || f.movement < float64(f.intervals)*minShare*(1-cutSlack) {
			continue
		}
		slices.Sort(f.rates)
		p90 := f.rates[(9*len(f.rates)+9)/10-1]
		best, measured = max(best, f.movement/f.tokens, p90), true
	}
	return best * 1e6, measured
}

// describeVotes lists every vote but the one at skip.
func describeVotes(votes []windowVote, skip int) string {
	var parts []string
	for i, v := range votes {
		if i != skip {
			parts = append(parts, v.describe())
		}
	}
	return strings.Join(parts, ", ")
}

// tierSettings are cfg's session tiers and balance settings, each invalid or
// missing value replaced by its default.
func tierSettings(cfg *config.Config) (config.SessionTiers, config.Balance) {
	tiers, balance := config.DefaultSessionTiers(), config.DefaultBalance()
	if cfg == nil {
		return tiers, balance
	}
	t, b := cfg.Limits.SessionsByTier, cfg.Balance
	for _, p := range []struct{ got, into *int }{{&t.Small, &tiers.Small}, {&t.Medium, &tiers.Medium}, {&t.Large, &tiers.Large}} {
		if *p.got > 0 {
			*p.into = *p.got
		}
	}
	if b.MinIntervalShare > 0 && b.MinIntervalShare <= 1 {
		balance.MinIntervalShare = b.MinIntervalShare
	}
	if b.Margin >= 0 && b.Margin < 1 && cfg.Balance != (config.Balance{}) {
		balance.Margin = b.Margin
	}
	return tiers, balance
}

func formatRate(perMillion float64) string {
	return strconv.FormatFloat(perMillion, 'g', 3, 64)
}

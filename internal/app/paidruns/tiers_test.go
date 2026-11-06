package paidruns

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

func tierConfig() *config.Config {
	cfg := testConfig()
	cfg.Limits.SessionsByTier = config.DefaultSessionTiers()
	cfg.Limits.MaxTotalSessions = 10
	cfg.Balance = config.DefaultBalance()
	return cfg
}

// rate is a qualified sample of agent's family in an account window kind,
// at share per million weighted tokens, over intervals intervals that each
// moved 6 % of the window.
func rate(agent, family, kind string, perMillion float64, intervals int) RateSample {
	movement := 0.06 * float64(intervals)
	return RateSample{Agent: agent, Family: family, Fingerprint: "fp", Window: WindowKey{Kind: kind},
		Movement: movement, WeightedTokens: movement / perMillion * 1e6, Intervals: intervals}
}

// rates are qualified opus samples of Claude in the five-hour and weekly
// windows of windows().
func rates(fiveHour, weekly float64) []RateSample {
	return []RateSample{rate("claude", "opus", "session", fiveHour, 3), rate("claude", "opus", "weekly_all", weekly, 3)}
}

func subscriber(agent string) Assessment {
	return Assessment{Agent: agent, Shape: workerenv.Worker, Verdict: Subscription, Account: "personal", UsableShare: 1}
}

func TestSessionTierMeasuredShareVotes(t *testing.T) {
	cfg := tierConfig()
	large := TierResult{Tier: TierLarge}
	claude := subscriber("claude")

	// Previous large: a step down applies at once, so the vote shows plainly.
	for _, c := range []struct {
		fiveHour, weekly float64
		want             string
	}{
		{.07, .001, TierSmall},
		{.065, .001, TierMedium}, // at the cut: the larger tier
		{.03, .001, TierMedium},
		{.015, .003, TierLarge}, // both at their large cuts
		{.01, .0031, TierMedium},
		{.01, .013, TierMedium},
		{.01, .0131, TierSmall},
		{.001, .001, TierLarge},
	} {
		got := SelectTier(claude, snapshot("claude"), rates(c.fiveHour, c.weekly), large, cfg)
		if got.Tier != c.want {
			t.Errorf("five-hour %v, weekly %v: tier %q (%s), want %q", c.fiveHour, c.weekly, got.Tier, got.Reason, c.want)
		}
		if math.Abs(got.Rates["session"]-c.fiveHour) > 1e-12 || math.Abs(got.Rates["weekly_all"]-c.weekly) > 1e-12 {
			t.Errorf("rates %v, want share per million %v and %v", got.Rates, c.fiveHour, c.weekly)
		}
	}

	// Limits follow the tier and the configured counts.
	for tier, want := range map[string]int{TierSmall: 3, TierMedium: 5, TierLarge: 10} {
		fiveHour := map[string]float64{TierSmall: .1, TierMedium: .03, TierLarge: .001}[tier]
		if got := SelectTier(claude, snapshot("claude"), rates(fiveHour, .001), large, cfg); got.Tier != tier || got.Limit != want {
			t.Errorf("%s: got %q limit %d, want limit %d", tier, got.Tier, got.Limit, want)
		}
	}
	low := tierConfig()
	low.Limits.SessionsByTier = config.SessionTiers{Small: 1, Medium: 2, Large: 4}
	if got := SelectTier(claude, snapshot("claude"), rates(.001, .001), large, low); got.Limit != 4 {
		t.Errorf("configured large limit = %d, want 4", got.Limit)
	}

	// The family with the highest qualified rate decides a window; an
	// unqualified sample, or another agent's, measures nothing.
	samples := append(rates(.01, .001), rate("claude", "sonnet", "session", .07, 3))
	if got := SelectTier(claude, snapshot("claude"), samples, large, cfg); got.Tier != TierSmall || math.Abs(got.Rates["session"]-.07) > 1e-12 {
		t.Errorf("highest family rate: %+v, want small at 0.07", got)
	}
	samples = append(rates(.01, .001), rate("claude", "sonnet", "session", .07, 2), rate("codex", "sol", "session", .5, 9))
	if got := SelectTier(claude, snapshot("claude"), samples, large, cfg); got.Tier != TierLarge {
		t.Errorf("unqualified and foreign samples counted: %+v", got)
	}

	// Three intervals are needed, each of at least min_interval_share.
	for _, samples := range [][]RateSample{
		{rate("claude", "opus", "session", .001, 2), rate("claude", "opus", "weekly_all", .001, 3)},
		{rate("claude", "opus", "session", .001, 3), func() RateSample {
			r := rate("claude", "opus", "weekly_all", .001, 3)
			r.Movement, r.WeightedTokens = .12, .12/.001*1e6 // 4 % per interval
			return r
		}()},
	} {
		if got := SelectTier(claude, snapshot("claude"), samples, large, cfg); got.Tier != TierSmall || !strings.Contains(got.Reason, "unmeasured") {
			t.Errorf("thin evidence qualified: %+v", got)
		}
	}
	strict := tierConfig()
	strict.Balance.MinIntervalShare = .1
	if got := SelectTier(claude, snapshot("claude"), rates(.001, .001), large, strict); got.Tier != TierSmall {
		t.Errorf("6 %% intervals qualified under a 10 %% minimum: %+v", got)
	}

	// A present unmeasured window votes small; an absent one abstains; no
	// window at all is small.
	got := SelectTier(claude, snapshot("claude"), rates(.001, .001)[:1], large, cfg)
	if got.Tier != TierSmall || !strings.Contains(got.Reason, "unmeasured") || !strings.Contains(got.Reason, "weekly_all") {
		t.Errorf("unmeasured weekly window: %+v, want small naming weekly_all", got)
	}
	weeklyOnly := snapshot("claude")
	weeklyOnly.Windows = weeklyOnly.Windows[1:]
	if got := SelectTier(claude, weeklyOnly, rates(.07, .001), large, cfg); got.Tier != TierLarge {
		t.Errorf("absent five-hour window voted: %+v", got)
	}
	none := snapshot("claude")
	none.Windows = nil
	if got := SelectTier(claude, none, rates(.001, .001), large, cfg); got.Tier != TierSmall {
		t.Errorf("no windows: %+v, want small", got)
	}

	// A window limited to one family does not vote for the whole agent.
	scoped := snapshot("claude")
	scoped.Windows = append(scoped.Windows, quota.Window{Kind: "weekly_opus", DurationMinutes: 10080, UsedPercent: 50, ResetsAt: now.Add(72 * time.Hour)})
	scoped.ModelLimits = map[string][]quota.Window{"sonnet": {{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 5, ResetsAt: now.Add(72 * time.Hour)}}}
	if got := SelectTier(claude, scoped, rates(.001, .001), large, cfg); got.Tier != TierLarge {
		t.Errorf("family-scoped windows voted: %+v", got)
	}

	// A window of unknown length takes the stricter weekly cuts.
	unknown := snapshot("claude")
	unknown.Windows = []quota.Window{{Kind: "odd", UsedPercent: 1, ResetsAt: now.Add(time.Hour)}}
	if got := SelectTier(claude, unknown, []RateSample{rate("claude", "opus", "odd", .02, 3)}, large, cfg); got.Tier != TierSmall {
		t.Errorf("unknown-length window at 0.02: %+v, want small by the weekly cut", got)
	}

	// Paid and workspace hold small whatever the rates; overage keeps the
	// measured tier; an unknown agent is small.
	codex := subscriber("codex")
	codexRates := []RateSample{rate("codex", "sol", "session", .001, 3), rate("codex", "sol", "weekly_all", .001, 3)}
	for name, c := range map[string]struct {
		a      Assessment
		tier   string
		reason string
	}{
		"paid":      {Assessment{Agent: "codex", Verdict: Paid, Account: "personal", UsableShare: 1}, TierSmall, "paid"},
		"workspace": {Assessment{Agent: "codex", Verdict: Subscription, Account: "workspace", UsableShare: .5}, TierSmall, "workspace"},
		"overage":   {Assessment{Agent: "codex", Verdict: SubscriptionOverage, Account: "personal", UsableShare: 1}, TierLarge, "weekly_all"},
		"plain":     {codex, TierLarge, "session"},
		"unknown":   {Assessment{Agent: "gemini", Verdict: Subscription, Account: "personal", UsableShare: 1}, TierSmall, "unknown agent"},
	} {
		got := SelectTier(c.a, snapshot("codex"), codexRates, large, cfg)
		if got.Tier != c.tier || !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: %+v, want %s with %q", name, got, c.tier, c.reason)
		}
		if got.Limit != cfg.Limits.SessionsByTier.Limit(c.tier) {
			t.Errorf("%s: limit %d", name, got.Limit)
		}
	}

	// No config falls back to the defaults.
	if got := SelectTier(claude, snapshot("claude"), rates(.001, .001), large, nil); got.Tier != TierLarge || got.Limit != 10 {
		t.Errorf("nil config: %+v", got)
	}
}

func TestTierUpgradeMarginAndImmediateDowngrade(t *testing.T) {
	cfg := tierConfig()
	claude := subscriber("claude")
	small, medium, large := TierResult{Tier: TierSmall}, TierResult{Tier: TierMedium}, TierResult{Tier: TierLarge}
	for _, c := range []struct {
		name             string
		previous         TierResult
		fiveHour, weekly float64
		want             string
		reason           []string
	}{
		{"inside the margin stays", small, .06, .001, TierSmall, []string{"session", "0.06"}},
		{"at the margin steps up", small, .052, .001, TierMedium, nil},
		{"far below steps two", small, .01, .001, TierLarge, nil},
		{"partly up", small, .013, .001, TierMedium, nil},
		{"every window must agree", medium, .01, .0028, TierMedium, []string{"weekly_all", "0.0028"}},
		{"all agree", medium, .01, .002, TierLarge, nil},
		{"large stays inside margin", medium, .014, .001, TierMedium, []string{"session", "0.014"}},
		{"down one at once", large, .016, .001, TierMedium, []string{"session", "0.016"}},
		{"down two at once", large, .07, .001, TierSmall, []string{"session", "0.07"}},
		{"weekly down at once", large, .001, .02, TierSmall, []string{"weekly_all", "0.02"}},
		{"unknown previous is small", TierResult{Tier: "huge"}, .06, .001, TierSmall, nil},
		{"no previous is small", TierResult{}, .06, .001, TierSmall, nil},
		{"no previous steps up past the margin", TierResult{}, .03, .001, TierMedium, nil},
	} {
		got := SelectTier(claude, snapshot("claude"), rates(c.fiveHour, c.weekly), c.previous, cfg)
		if got.Tier != c.want {
			t.Errorf("%s: tier %q (%s), want %q", c.name, got.Tier, got.Reason, c.want)
		}
		for _, part := range c.reason {
			if !strings.Contains(got.Reason, part) {
				t.Errorf("%s: reason %q lacks %q", c.name, got.Reason, part)
			}
		}
		if !strings.Contains(got.Reason, "share/M") {
			t.Errorf("%s: reason %q gives no rate in share/M", c.name, got.Reason)
		}
	}

	// A larger margin delays the step up.
	wide := tierConfig()
	wide.Balance.Margin = .5
	if got := SelectTier(claude, snapshot("claude"), rates(.04, .001), small, wide); got.Tier != TierSmall {
		t.Errorf("margin 0.5 at 0.04: %+v, want small", got)
	}
	if got := SelectTier(claude, snapshot("claude"), rates(.03, .001), small, wide); got.Tier != TierMedium {
		t.Errorf("margin 0.5 at 0.03: %+v, want medium", got)
	}

	// A fingerprint change or a drift reset drops the samples: small again.
	got := SelectTier(claude, snapshot("claude"), nil, large, cfg)
	if got.Tier != TierSmall || !strings.Contains(got.Reason, "unmeasured") {
		t.Errorf("after a reset: %+v, want small and unmeasured", got)
	}

	// The forced reasons name their cause; Live is the caller's.
	paid := claude
	paid.Verdict = Paid
	got = SelectTier(paid, snapshot("claude"), rates(.001, .001), TierResult{Tier: TierLarge, Live: 2}, cfg)
	if got.Tier != TierSmall || !strings.HasPrefix(got.Reason, "paid") || got.Live != 2 {
		t.Errorf("paid: %+v", got)
	}
	work := claude
	work.Account = "workspace"
	if got = SelectTier(work, snapshot("claude"), rates(.001, .001), large, cfg); got.Tier != TierSmall || !strings.HasPrefix(got.Reason, "workspace") {
		t.Errorf("workspace: %+v", got)
	}
}

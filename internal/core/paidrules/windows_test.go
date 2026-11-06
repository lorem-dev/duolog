package paidrules

import (
	"math"
	"testing"
	"time"

	coreaccount "github.com/lorem-dev/duolog/internal/core/account"
)

// codexScoped is a Codex read with account windows and per-family limits:
// luna's own limit is rejected, sol's is at 40 %, and "codex" names no one
// family.
func codexScoped() coreaccount.Snapshot {
	s := snapshot("codex")
	week := now.Add(72 * time.Hour)
	s.ModelLimits = map[string][]coreaccount.Window{
		"luna":  {{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 20, ResetsAt: week, Severity: "rejected"}},
		"sol":   {{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 40, ResetsAt: week}},
		"codex": {{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 25, ResetsAt: week}},
	}
	return s
}

func costOf(t *testing.T, costs []WindowCost, kind, family string) WindowCost {
	t.Helper()
	for _, c := range costs {
		if c.Key.Kind == kind && c.Key.Family == family {
			return c
		}
	}
	t.Fatalf("no %s/%s window in %+v", kind, family, costs)
	return WindowCost{}
}

func hasWindow(costs []WindowCost, family string) bool {
	for _, c := range costs {
		if c.Key.Family == family {
			return true
		}
	}
	return false
}

// task weighs 1.5M tokens: 1M input, cached included at full weight, and
// 100K output at five times the input weight.
var task = Estimate{Input: 1000000, Cached: 400000, Output: 100000, USD: 2.5, Source: "test"}

func sample(family, kind string, movement float64) RateSample {
	return RateSample{Agent: "codex", Family: family, Fingerprint: "f",
		Window: WindowKey{Kind: kind}, Movement: movement, WeightedTokens: 1.5e6, Intervals: 1}
}

func TestUsableHeadroomReservesAndScopedLimits(t *testing.T) {
	t.Run("scaled admission ceiling", func(t *testing.T) {
		weekly := func(used, reserved, cost float64) []WindowCost {
			return []WindowCost{{Key: WindowKey{Kind: "weekly_all"}, Used: used, Reserved: reserved, Cost: cost, Reserve: 0.15}}
		}
		five := func(total float64) []WindowCost {
			return []WindowCost{{Key: WindowKey{Kind: "session"}, Used: total, Reserve: 0.10}}
		}
		cases := []struct {
			name  string
			costs []WindowCost
			u     float64
			want  bool
		}{
			{"workspace weekly at .425", weekly(0.2, 0.1, 0.125), 0.5, true},
			{"workspace weekly past .425", weekly(0.2, 0.1, 0.126), 0.5, false},
			{"reserved counts", weekly(0.2, 0.2, 0.125), 0.5, false},
			{"workspace five-hour at .45", five(0.45), 0.5, true},
			{"workspace five-hour past .45", five(0.451), 0.5, false},
			{"personal weekly at .85", weekly(0.6, 0.15, 0.1), 1, true},
			{"personal weekly past .85", weekly(0.6, 0.15, 0.101), 1, false},
			{"personal five-hour at .9", five(0.9), 1, true},
			{"every window", append(weekly(0.1, 0, 0.01), five(0.95)...), 1, false},
			{"no windows", nil, 1, false},
			{"zero share", weekly(0.1, 0, 0.01), 0, false},
			{"negative share", weekly(0.1, 0, 0.01), -0.5, false},
			{"share above one", weekly(0.1, 0, 0.01), 1.1, false},
			{"NaN share", weekly(0.1, 0, 0.01), math.NaN(), false},
			{"infinite share", weekly(0.1, 0, 0.01), math.Inf(1), false},
			{"NaN used", weekly(math.NaN(), 0, 0.01), 1, false},
			{"infinite cost", weekly(0.1, 0, math.Inf(1)), 1, false},
			{"negative used", weekly(-0.5, 0, 0.01), 1, false},
			{"negative reserved", weekly(0.5, -0.4, 0.01), 1, false},
			{"negative cost", weekly(0.5, 0, -0.4), 1, false},
			{"negative reserve", []WindowCost{{Used: 0.9, Reserve: -0.5}}, 1, false},
		}
		for _, c := range cases {
			if got := FitsWindows(c.costs, c.u); got != c.want {
				t.Errorf("%s: FitsWindows = %v, want %v", c.name, got, c.want)
			}
		}
	})

	t.Run("raw fractions, bounds and reserves", func(t *testing.T) {
		s := snapshot("codex")
		before := task
		costs, err := WindowCosts(s, "sol", task, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(costs) != 2 {
			t.Fatalf("costs = %+v", costs)
		}
		w := costOf(t, costs, "weekly_all", "")
		f := costOf(t, costs, "session", "")
		if w.Used != 0.3 || w.Reserved != 0 || w.Cost != 0.01 || w.Reserve != 0.15 || !w.Key.Reset.Equal(now.Add(72*time.Hour)) {
			t.Fatalf("weekly = %+v", w)
		}
		if f.Used != 0.12 || f.Reserved != 0 || f.Cost != 0.05 || f.Reserve != 0.10 {
			t.Fatalf("five-hour = %+v", f)
		}
		// USD is money: window math neither reads nor scales it.
		if task.USD != before.USD || task.USD != 2.5 {
			t.Fatalf("estimate changed: %+v", task)
		}
		if !FitsWindows(costs, 1) || !FitsWindows(costs, 0.5) {
			t.Fatal("headroom refused")
		}
	})

	t.Run("scoped limits", func(t *testing.T) {
		s := codexScoped()
		costs, err := WindowCosts(s, "sol", task, nil)
		if err != nil {
			t.Fatal(err)
		}
		if hasWindow(costs, "luna") {
			t.Fatalf("luna's limit applies to sol: %+v", costs)
		}
		if c := costOf(t, costs, "weekly_all", "sol"); c.Used != 0.4 {
			t.Fatalf("sol = %+v", c)
		}
		costOf(t, costs, "weekly_all", "codex")
		// An unrelated family's rejection does not block, where
		// Snapshot.Healthy would refuse every family.
		if s.Healthy(now) || !FitsWindows(costs, 1) {
			t.Fatal("unrelated rejection blocked sol")
		}
		costs, err = WindowCosts(s, "luna", task, nil)
		if err != nil {
			t.Fatal(err)
		}
		if c := costOf(t, costs, "weekly_all", "luna"); c.Used != 1 {
			t.Fatalf("rejected luna = %+v", c)
		}
		if FitsWindows(costs, 1) {
			t.Fatal("luna's own rejection did not block")
		}

		// A full family window of Claude blocks that family only; a
		// window scoped to a model name it does not know applies to all.
		c := snapshot("claude")
		week := now.Add(72 * time.Hour)
		c.Windows = append(c.Windows,
			coreaccount.Window{Kind: "weekly_opus", Group: "weekly", DurationMinutes: 10080, UsedPercent: 100, ResetsAt: week},
			coreaccount.Window{Kind: "weekly_scoped", Group: "weekly", Model: "opus 5", DurationMinutes: 10080, UsedPercent: 95, ResetsAt: week},
			coreaccount.Window{Kind: "weekly_scoped", Group: "weekly", Model: "nova", DurationMinutes: 10080, UsedPercent: 10, ResetsAt: week})
		sonnet, err := WindowCosts(c, "sonnet", task, nil)
		if err != nil {
			t.Fatal(err)
		}
		if hasWindow(sonnet, "opus") || !hasWindow(sonnet, "nova") || !FitsWindows(sonnet, 1) {
			t.Fatalf("sonnet = %+v", sonnet)
		}
		opus, err := WindowCosts(c, "opus", task, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(opus) != 5 || FitsWindows(opus, 1) {
			t.Fatalf("opus = %+v", opus)
		}

		for name, blocked := range map[string]func(*coreaccount.Snapshot){
			"account rejected":      func(s *coreaccount.Snapshot) { s.Rejected = true },
			"spend control reached": func(s *coreaccount.Snapshot) { s.SpendControlReached = true },
		} {
			s := codexScoped()
			blocked(&s)
			if costs, err := WindowCosts(s, "sol", task, nil); err == nil {
				t.Fatalf("%s: costs %+v", name, costs)
			}
		}
	})

	t.Run("overage never bypasses the reserve", func(t *testing.T) {
		s := snapshot("codex")
		// The weekly window has room for the task, but only inside the
		// lead's reserve: .845 used + .01 cost + .15 reserve > 1.
		s.Windows[1].UsedPercent = 84.5
		s.Credits = &coreaccount.Credits{HasCredits: true, Unlimited: true}
		s.Billing.OverageEnabled = true
		s.ExtraUsage = &coreaccount.ExtraUsage{Enabled: true}
		costs, err := WindowCosts(s, "sol", task, nil)
		if err != nil {
			t.Fatal(err)
		}
		w := costOf(t, costs, "weekly_all", "")
		if w.Reserve != 0.15 || w.Used+w.Cost > 1 || FitsWindows(costs, 1) {
			t.Fatalf("weekly = %+v admitted into the reserve", w)
		}
	})

	t.Run("absent or expired evidence", func(t *testing.T) {
		refuse := map[string]func(*coreaccount.Snapshot){
			"no windows":     func(s *coreaccount.Snapshot) { s.Windows = nil },
			"never read":     func(s *coreaccount.Snapshot) { s.ReadAt = time.Time{} },
			"all expired":    func(s *coreaccount.Snapshot) { s.ReadAt = now.Add(100 * time.Hour) },
			"no reset times": func(s *coreaccount.Snapshot) { s.Windows[0].ResetsAt, s.Windows[1].ResetsAt = time.Time{}, time.Time{} },
			"NaN used":       func(s *coreaccount.Snapshot) { s.Windows[0].UsedPercent = math.NaN() },
			"infinite used":  func(s *coreaccount.Snapshot) { s.Windows[1].UsedPercent = math.Inf(1) },
			"negative used":  func(s *coreaccount.Snapshot) { s.Windows[0].UsedPercent = -1 },
			"used above 100": func(s *coreaccount.Snapshot) { s.Windows[1].UsedPercent = 101 },
			"bad family used": func(s *coreaccount.Snapshot) {
				s.ModelLimits = map[string][]coreaccount.Window{"sol": {{UsedPercent: math.NaN()}}}
			},
		}
		for name, change := range refuse {
			s := snapshot("codex")
			change(&s)
			if costs, err := WindowCosts(s, "sol", task, nil); err == nil {
				t.Errorf("%s: costs %+v", name, costs)
			}
		}

		// A window that reset before the read is not free headroom: it
		// counts as full until a read shows the new window.
		s := snapshot("codex")
		s.Windows[0].ResetsAt = s.ReadAt.Add(-time.Minute)
		costs, err := WindowCosts(s, "sol", task, nil)
		if err != nil {
			t.Fatal(err)
		}
		if f := costOf(t, costs, "session", ""); f.Used != 1 || FitsWindows(costs, 1) {
			t.Fatalf("expired five-hour = %+v", f)
		}

		bad := []Estimate{
			{Input: -1, Output: 10},
			{Input: 10, Cached: 11, Output: 10},
			{Input: 10, Output: -1},
			{Input: 10, Output: 10, USD: math.NaN()},
			{Input: 10, Output: 10, USD: -1},
		}
		for _, e := range bad {
			if _, err := WindowCosts(snapshot("codex"), "sol", e, nil); err == nil {
				t.Errorf("estimate %+v accepted", e)
			}
		}
	})

	t.Run("real-unit rates and measured floors", func(t *testing.T) {
		s := snapshot("codex")
		weekly := func(samples []RateSample) float64 {
			t.Helper()
			costs, err := WindowCosts(s, "sol", task, samples)
			if err != nil {
				t.Fatal(err)
			}
			if f := costOf(t, costs, "session", ""); f.Cost != 0.05 {
				t.Fatalf("unmeasured five-hour = %+v", f)
			}
			return costOf(t, costs, "weekly_all", "").Cost
		}
		unrelated := []RateSample{
			sample("luna", "weekly_all", 0.9),
			sample("sol", "session", 0.0001),
			{Family: "sol", Window: WindowKey{Kind: "weekly_all", Family: "sol"}, Movement: 0.9, WeightedTokens: 1.5e6, Intervals: 5},
		}
		three := []RateSample{sample("sol", "weekly_all", 0.002), sample("sol", "weekly_all", 0.004), sample("sol", "weekly_all", 0.006)}

		if got := weekly(unrelated[:1]); got != 0.01 {
			t.Fatalf("unrelated samples moved the bound: %v", got)
		}
		// Fewer than three measurements keep the bound unless one shows more.
		if got := weekly(three[:2]); got != 0.01 {
			t.Fatalf("two measurements: %v", got)
		}
		if got := weekly([]RateSample{sample("sol", "weekly_all", 0.03)}); !near(got, 0.03) {
			t.Fatalf("one larger measurement: %v", got)
		}
		// Three measurements: the pooled rate (.004 for this task), never
		// below their p90 (.006).
		if got := weekly(append(three, unrelated...)); !near(got, 0.006) {
			t.Fatalf("three measurements: %v", got)
		}
		// One stored sample of three intervals counts as three.
		pooled := sample("sol", "weekly_all", 0.012)
		pooled.WeightedTokens, pooled.Intervals = 4.5e6, 3
		if got := weekly([]RateSample{pooled}); !near(got, 0.004) {
			t.Fatalf("pooled sample: %v", got)
		}
		// A measurement of nothing measures nothing.
		zero := []RateSample{sample("sol", "weekly_all", 0), sample("sol", "weekly_all", 0), sample("sol", "weekly_all", 0)}
		if got := weekly(zero); got != 0.01 {
			t.Fatalf("zero movement: %v", got)
		}

		for name, bad := range map[string]RateSample{
			"NaN movement":        {Family: "sol", Window: WindowKey{Kind: "weekly_all"}, Movement: math.NaN(), WeightedTokens: 1, Intervals: 1},
			"negative movement":   {Family: "sol", Window: WindowKey{Kind: "weekly_all"}, Movement: -0.1, WeightedTokens: 1, Intervals: 1},
			"infinite tokens":     {Family: "sol", Window: WindowKey{Kind: "weekly_all"}, Movement: 0.1, WeightedTokens: math.Inf(1), Intervals: 1},
			"negative tokens":     {Family: "sol", Window: WindowKey{Kind: "weekly_all"}, Movement: 0.1, WeightedTokens: -1, Intervals: 1},
			"negative intervals":  {Family: "sol", Window: WindowKey{Kind: "weekly_all"}, Movement: 0.1, WeightedTokens: 1, Intervals: -1},
			"other family broken": {Family: "luna", Window: WindowKey{Kind: "weekly_all"}, Movement: math.NaN(), WeightedTokens: 1, Intervals: 1},
		} {
			if costs, err := WindowCosts(s, "sol", task, []RateSample{bad}); err == nil {
				t.Errorf("%s: costs %+v", name, costs)
			}
		}
	})
}

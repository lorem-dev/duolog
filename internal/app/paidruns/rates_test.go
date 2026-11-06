package paidruns

import (
	"context"
	"math"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/quota"
)

var (
	sessionWindow = paidrules.WindowKey{Kind: "session"}
	weeklyWindow  = paidrules.WindowKey{Kind: "weekly_all"}
)

// rateRead is a Claude read m minutes after now: the five-hour window at
// session percent and the weekly window at weekly percent.
func rateRead(m int, session, weekly float64) quota.Snapshot {
	s := snapshot("claude")
	s.ReadAt = now.Add(time.Duration(m) * time.Minute)
	s.Windows = []quota.Window{
		{Kind: "session", DurationMinutes: 300, UsedPercent: session, ResetsAt: now.Add(4 * time.Hour)},
		{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: weekly, ResetsAt: now.Add(72 * time.Hour)},
	}
	return s
}

// learner observes a chain of Claude sonnet reads on one store.
type learner struct {
	t    *testing.T
	s    *Store
	cfg  *config.Config
	key  string
	plan string // the plan of climb's reads, "" for rateRead's
	last quota.Snapshot
}

func newLearner(t *testing.T, cfg *config.Config) *learner {
	t.Helper()
	clock := now
	return &learner{t: t, s: testStore(t, &clock), cfg: cfg, key: "key-a", last: rateRead(0, 0, 0)}
}

// observe reports the interval from before to after with input tokens of
// sonnet, none cached and no output, so they weigh input.
func (l *learner) observe(before, after quota.Snapshot, input int64) {
	l.t.Helper()
	in := paidrules.Interval{Agent: "claude", Family: "sonnet", Fingerprint: paidrules.Fingerprint("claude", l.key, after),
		Before: before, After: after, Input: input}
	if err := l.s.Observe(context.Background(), l.cfg, in); err != nil {
		l.t.Fatal(err)
	}
}

// step observes the interval from the last read to next.
func (l *learner) step(next quota.Snapshot, input int64) {
	l.t.Helper()
	l.observe(l.last, next, input)
	l.last = next
}

func (l *learner) state() State {
	l.t.Helper()
	var got State
	if err := l.s.Update(context.Background(), func(st *State) error {
		got = *st
		return nil
	}); err != nil {
		l.t.Fatal(err)
	}
	return got
}

func (l *learner) learned(window paidrules.WindowKey) paidrules.LearnedRate {
	l.t.Helper()
	return paidrules.Learned(l.state().Samples, "claude", "sonnet", paidrules.Fingerprint("claude", l.key, l.last), window)
}

// climb steps n times, the session window by session points and the weekly
// one by weekly points each time, with input tokens per step.
func (l *learner) climb(n int, session, weekly float64, input int64) {
	l.t.Helper()
	for range n {
		w := l.last.Windows
		minute := int(l.last.ReadAt.Sub(now) / time.Minute)
		next := rateRead(minute+10, w[0].UsedPercent+session, w[1].UsedPercent+weekly)
		if l.plan != "" {
			next.Plan = l.plan
		}
		l.step(next, input)
	}
}

func TestRatesRealUnitsAndThreeIntervals(t *testing.T) {
	t.Run("weighted tokens price cached and output against input", func(t *testing.T) {
		cfg := pricedConfig()
		got, err := paidrules.WeightedTokens(cfg, "claude", "sonnet", 1000000, 400000, 100000)
		if err != nil {
			t.Fatal(err)
		}
		// sonnet: input 2, cached 0.1, output 10.
		want := 1000000 + 400000*(0.1/2-1) + 100000*10.0/2
		if !near(got, want) {
			t.Fatalf("weighted = %v, want %v", got, want)
		}
		concrete, err := paidrules.WeightedTokens(cfg, "claude", "claude-sonnet-5-5", 1000000, 400000, 100000)
		if err != nil || !near(concrete, want) {
			t.Fatalf("concrete model weighted = %v, %v; want %v", concrete, err, want)
		}

		price := func(edit func(*config.Model)) *config.Config {
			c := pricedConfig()
			m := c.Models["sonnet"]
			edit(&m)
			c.Models["sonnet"] = m
			return c
		}
		bad := []struct {
			name                  string
			cfg                   *config.Config
			agent, family         string
			input, cached, output int64
		}{
			{"cached above input", cfg, "claude", "sonnet", 10, 11, 0},
			{"negative input", cfg, "claude", "sonnet", -1, 0, 0},
			{"negative cached", cfg, "claude", "sonnet", 10, -1, 0},
			{"negative output", cfg, "claude", "sonnet", 10, 0, -1},
			{"unpriced family", cfg, "claude", "haiku", 10, 0, 0},
			{"another agent's family", cfg, "codex", "sonnet", 10, 0, 0},
			{"zero input price", price(func(m *config.Model) { m.Input = 0 }), "claude", "sonnet", 10, 0, 0},
			{"zero output price", price(func(m *config.Model) { m.Output = 0 }), "claude", "sonnet", 10, 0, 0},
			{"negative cached price", price(func(m *config.Model) { m.Cached = -1 }), "claude", "sonnet", 10, 0, 0},
			{"NaN cached price", price(func(m *config.Model) { m.Cached = math.NaN() }), "claude", "sonnet", 10, 0, 0},
			{"no config", nil, "claude", "sonnet", 10, 0, 0},
		}
		for _, c := range bad {
			if w, err := paidrules.WeightedTokens(c.cfg, c.agent, c.family, c.input, c.cached, c.output); err == nil {
				t.Errorf("%s: weighted = %v, want an error", c.name, w)
			}
		}

		l := newLearner(t, cfg)
		in := paidrules.Interval{Agent: "claude", Family: "sonnet", Fingerprint: paidrules.Fingerprint("claude", l.key, rateRead(10, 9, 0)),
			Before: l.last, After: rateRead(10, 9, 0), Input: 10, Cached: 11}
		if err := l.s.Observe(context.Background(), cfg, in); err == nil {
			t.Fatal("an invalid cached subset was observed")
		}
		in.Cached, in.Family = 0, "haiku"
		if err := l.s.Observe(context.Background(), cfg, in); err == nil {
			t.Fatal("an unpriced family was observed")
		}
		if got := l.state().Samples; len(got) != 0 {
			t.Fatalf("a refused interval left samples %+v", got)
		}
	})

	t.Run("three qualified intervals per family and window", func(t *testing.T) {
		l := newLearner(t, pricedConfig())
		// Each step moves the five-hour window 3 points and the weekly one 1
		// point for 1M weighted tokens: the five-hour window qualifies every
		// second step, the weekly one at its fifth.
		l.climb(5, 3, 1, 1000000)
		if r := l.learned(sessionWindow); r.Measured || r.Intervals != 2 {
			t.Fatalf("after two intervals: %+v, want 2 unmeasured", r)
		}
		l.climb(1, 3, 1, 1000000)
		r := l.learned(sessionWindow)
		if !r.Measured || r.Intervals != 3 || !near(r.PerToken*1e6, 0.03) {
			t.Fatalf("five-hour rate %+v, want measured 0.03 per million over 3 intervals", r)
		}
		if w := l.learned(weeklyWindow); w.Measured || w.Intervals != 1 || !near(w.PerToken*1e6, 0.01) {
			t.Fatalf("weekly rate %+v, want 1 unmeasured interval at 0.01 per million", w)
		}
		if o := paidrules.Learned(l.state().Samples, "claude", "opus", paidrules.Fingerprint("claude", l.key, l.last), sessionWindow); o.Measured || o.Intervals != 0 {
			t.Fatalf("opus has rate %+v without opus tokens", o)
		}
		for _, s := range l.state().Samples {
			// Fractions of the window, not percentage points.
			if s.Agent != "claude" || s.Family != "sonnet" || s.Intervals != 1 || s.Movement > 0.1 || s.WeightedTokens <= 0 {
				t.Fatalf("sample %+v", s)
			}
			if s.Window.Kind == "session" && (!near(s.Movement, 0.06) || !near(s.WeightedTokens, 2000000)) {
				t.Fatalf("five-hour sample %+v, want 0.06 over 2M", s)
			}
		}
	})

	t.Run("duplicate and overlapping endpoints count once", func(t *testing.T) {
		l := newLearner(t, pricedConfig())
		a, b := l.last, rateRead(10, 6, 0)
		l.observe(a, b, 1000000)
		l.observe(a, b, 1000000)
		l.observe(b, b, 1000000)
		l.observe(rateRead(5, 3, 0), b, 1000000)
		r := l.learned(sessionWindow)
		if r.Intervals != 1 {
			t.Fatalf("intervals %d, want the one interval once", r.Intervals)
		}
		st := l.state()
		if len(st.Samples) != 1 || !near(st.Samples[0].Movement, 0.06) || !near(st.Samples[0].WeightedTokens, 1000000) {
			t.Fatalf("samples %+v, want one of 0.06 over 1M", st.Samples)
		}
		// The next interval shares an endpoint and is independent.
		l.observe(b, rateRead(20, 12, 0), 1000000)
		if r := paidrules.Learned(l.state().Samples, "claude", "sonnet", paidrules.Fingerprint("claude", l.key, b), sessionWindow); r.Intervals != 2 {
			t.Fatalf("intervals %d after a following interval, want 2", r.Intervals)
		}
	})

	t.Run("usable share leaves rates in real units", func(t *testing.T) {
		workspace := pricedConfig()
		workspace.PaidRuns.Claude.Account = "workspace"
		workspace.PaidRuns.UsableShare = 0.5
		personal := pricedConfig()
		personal.PaidRuns.Claude.Account = "personal"
		personal.PaidRuns.UsableShare = 1
		w, p := newLearner(t, workspace), newLearner(t, personal)
		w.climb(6, 3, 1, 1000000)
		p.climb(6, 3, 1, 1000000)
		if ws, ps := w.state().Samples, p.state().Samples; !reflect.DeepEqual(ws, ps) {
			t.Fatalf("workspace samples %+v differ from personal %+v", ws, ps)
		}
		if wr, pr := w.learned(sessionWindow), p.learned(sessionWindow); !near(wr.PerToken, pr.PerToken) || !near(wr.PerToken*1e6, 0.03) {
			t.Fatalf("workspace rate %v, personal %v, want both 0.03 per million", wr.PerToken, pr.PerToken)
		}
	})

	t.Run("untracked usage raises the rate and invents no tokens", func(t *testing.T) {
		l := newLearner(t, pricedConfig())
		// The person's own work moves the window 4 points; duolog used nothing.
		l.step(rateRead(10, 4, 0), 0)
		if got := l.state().Samples; len(got) != 0 {
			t.Fatalf("movement without tokens made samples %+v", got)
		}
		// A task of 1M moves it 2 more: its rate carries the untracked 4.
		l.step(rateRead(20, 6, 0), 1000000)
		st := l.state()
		if len(st.Samples) != 1 || !near(st.Samples[0].Movement, 0.06) || !near(st.Samples[0].WeightedTokens, 1000000) {
			t.Fatalf("samples %+v, want 0.06 over the 1M tracked tokens", st.Samples)
		}
		for _, s := range st.Samples {
			if s.WeightedTokens == 0 {
				t.Fatalf("a sample without tokens: %+v", s)
			}
		}
	})
}

func TestRatesResetAndDrift(t *testing.T) {
	t.Run("rollover and falling usage never measure", func(t *testing.T) {
		l := newLearner(t, pricedConfig())
		l.step(rateRead(10, 3, 0), 1000000)
		// The five-hour window reset between the reads: 3 -> 90 is no movement.
		rolled := rateRead(20, 90, 0)
		rolled.Windows[0].ResetsAt = now.Add(9 * time.Hour)
		l.step(rolled, 1000000)
		next := rateRead(30, 93, 0)
		next.Windows[0].ResetsAt = rolled.Windows[0].ResetsAt
		l.step(next, 1000000)
		if r := l.learned(sessionWindow); r.Intervals != 0 {
			t.Fatalf("rollover measured %+v", r)
		}

		falling := newLearner(t, pricedConfig())
		falling.step(rateRead(10, 3, 0), 1000000)
		falling.step(rateRead(20, 2, 0), 1000000)
		falling.step(rateRead(30, 5, 0), 1000000)
		if r := falling.learned(sessionWindow); r.Intervals != 0 {
			t.Fatalf("falling usage measured %+v", r)
		}

		// The window's reset passed before the second read, though the read
		// still names it.
		stale := newLearner(t, pricedConfig())
		before := rateRead(0, 10, 0)
		before.Windows[0].ResetsAt = now.Add(15 * time.Minute)
		after := rateRead(20, 20, 0)
		after.Windows[0].ResetsAt = before.Windows[0].ResetsAt
		stale.observe(before, after, 1000000)
		if got := stale.state().Samples; len(got) != 0 {
			t.Fatalf("a read past the reset measured %+v", got)
		}

		// A rejected or full window hides how far usage went.
		full := newLearner(t, pricedConfig())
		full.last = rateRead(0, 92, 0)
		full.step(rateRead(10, 100, 0), 1000000)
		rejected := rateRead(20, 99, 0)
		rejected.Windows[0].Severity = "rejected"
		full.observe(rateRead(10, 92, 0), rejected, 1000000)
		if got := full.state().Samples; len(got) != 0 {
			t.Fatalf("a full window measured %+v", got)
		}
	})

	t.Run("identity change clears rates and tier and keeps money", func(t *testing.T) {
		l := newLearner(t, pricedConfig())
		l.climb(6, 3, 0, 1000000)
		codex := paidrules.RateSample{Agent: "codex", Family: "sol", Fingerprint: "c", Window: weeklyWindow,
			Movement: 0.06, WeightedTokens: 1e6, Intervals: 1}
		if err := l.s.Update(context.Background(), func(st *State) error {
			st.Reservations["r1"] = paidrules.Reservation{ID: "r1", Agent: "claude", Task: true, Created: now,
				Estimate: paidrules.Estimate{USD: 1.5}}
			st.Settled["s1"] = paidrules.Settlement{ID: "s1", Agent: "claude", ChargedUSD: 2, Ended: now}
			st.Tiers["claude"] = paidrules.TierResult{Tier: "large", Limit: 10}
			st.Tiers["codex"] = paidrules.TierResult{Tier: "medium", Limit: 5}
			st.Samples = append(st.Samples, codex)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if r := l.learned(sessionWindow); !r.Measured {
			t.Fatalf("not measured before the change: %+v", r)
		}
		before := l.state()

		l.key = "key-b"
		l.climb(1, 3, 0, 1000000)
		st := l.state()
		if r := l.learned(sessionWindow); r.Measured || r.Intervals != 0 {
			t.Fatalf("a new identity kept rate %+v", r)
		}
		for _, s := range st.Samples {
			if s.Agent == "claude" {
				t.Fatalf("claude sample kept: %+v", s)
			}
		}
		if _, ok := st.Tiers["claude"]; ok {
			t.Fatalf("claude tier kept: %+v", st.Tiers["claude"])
		}
		if !reflect.DeepEqual(st.Tiers["codex"], before.Tiers["codex"]) || !slicesHave(st.Samples, codex) {
			t.Fatal("another agent's tier or rates changed")
		}
		if !reflect.DeepEqual(st.Reservations, before.Reservations) || !reflect.DeepEqual(st.Settled, before.Settled) {
			t.Fatalf("money changed: %+v %+v", st.Reservations, st.Settled)
		}
	})

	t.Run("plan names only change the fingerprint", func(t *testing.T) {
		read := rateRead(0, 0, 0)
		fp := paidrules.Fingerprint("claude", "key-a", read)
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(fp) {
			t.Fatalf("fingerprint %q is not a digest", fp)
		}
		if paidrules.Fingerprint("claude", "key-a", read) != fp {
			t.Fatal("fingerprint is not stable")
		}
		other := read
		other.Plan = "pro"
		later := rateRead(90, 40, 20)
		for name, got := range map[string]string{
			"plan":     paidrules.Fingerprint("claude", "key-a", other),
			"identity": paidrules.Fingerprint("claude", "key-b", read),
			"agent":    paidrules.Fingerprint("codex", "key-a", read),
		} {
			if got == fp {
				t.Errorf("a changed %s keeps the fingerprint", name)
			}
		}
		if paidrules.Fingerprint("claude", "key-a", later) != fp {
			t.Fatal("usage or read time changed the fingerprint")
		}

		l := newLearner(t, pricedConfig())
		l.climb(6, 3, 0, 1000000)
		l.plan = "enterprise"
		l.climb(1, 3, 0, 1000000)
		st := l.state()
		if r := l.learned(sessionWindow); r.Measured || r.Intervals != 0 {
			t.Fatalf("a new plan kept rate %+v", r)
		}
		if len(st.Tiers) != 0 {
			t.Fatalf("a plan name chose a tier: %+v", st.Tiers)
		}
		// The rate learned on the new plan comes from movement only.
		l.climb(6, 3, 0, 1000000)
		if r := l.learned(sessionWindow); !r.Measured || !near(r.PerToken*1e6, 0.03) || len(l.state().Tiers) != 0 {
			t.Fatalf("rate %+v, tiers %+v", r, l.state().Tiers)
		}
	})

	t.Run("window shape change clears rates", func(t *testing.T) {
		l := newLearner(t, pricedConfig())
		l.climb(6, 3, 0, 1000000)
		// The weekly window is gone from the next read.
		next := rateRead(70, 21, 0)
		next.Windows = next.Windows[:1]
		if paidrules.Fingerprint("claude", l.key, next) == paidrules.Fingerprint("claude", l.key, l.last) {
			t.Fatal("a window shape change keeps the fingerprint")
		}
		l.step(next, 1000000)
		if got := l.state().Samples; len(got) != 0 {
			t.Fatalf("samples kept or measured across a shape change: %+v", got)
		}
		longer := rateRead(0, 0, 0)
		longer.Windows[0].DurationMinutes = 600
		if paidrules.Fingerprint("claude", l.key, longer) == paidrules.Fingerprint("claude", l.key, rateRead(0, 0, 0)) {
			t.Fatal("a window length change keeps the fingerprint")
		}
	})

	t.Run("sustained different rate resets before an upgrade", func(t *testing.T) {
		l := newLearner(t, pricedConfig())
		l.climb(6, 3, 0, 1000000)
		if err := l.s.Update(context.Background(), func(st *State) error {
			st.Tiers["claude"] = paidrules.TierResult{Tier: "small", Limit: 3}
			st.Reservations["r1"] = paidrules.Reservation{ID: "r1", Agent: "claude", Task: true, Created: now}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		// The window doubles: 3 points now take 2M tokens.
		for i, want := range []int{4, 5} {
			l.climb(2, 3, 0, 2000000)
			if r := l.learned(sessionWindow); !r.Measured || r.Intervals != want {
				t.Fatalf("after %d new intervals: %+v, want the old rate kept", i+1, r)
			}
		}
		l.climb(2, 3, 0, 2000000)
		st := l.state()
		if r := l.learned(sessionWindow); r.Measured || r.Intervals != 0 {
			t.Fatalf("three incompatible intervals kept rate %+v", r)
		}
		if _, ok := st.Tiers["claude"]; ok {
			t.Fatal("drift kept the tier")
		}
		if _, ok := st.Reservations["r1"]; !ok {
			t.Fatal("drift dropped a reservation")
		}
		// Three fresh intervals measure the new rate.
		l.climb(4, 3, 0, 2000000)
		if r := l.learned(sessionWindow); r.Measured {
			t.Fatalf("measured after two fresh intervals: %+v", r)
		}
		l.climb(2, 3, 0, 2000000)
		if r := l.learned(sessionWindow); !r.Measured || r.Intervals != 3 || !near(r.PerToken*1e6, 0.015) {
			t.Fatalf("fresh rate %+v, want 0.015 per million", r)
		}

		// Within the margin the rate stands.
		steady := newLearner(t, pricedConfig())
		steady.climb(6, 3, 0, 1000000)
		steady.climb(6, 3.3, 0, 1000000)
		if r := steady.learned(sessionWindow); !r.Measured || r.Intervals != 6 || !near(r.PerToken*1e6, 0.033) {
			t.Fatalf("steady rate %+v, want 6 intervals with upper 0.033 per million", r)
		}

		// A higher rate resets as well.
		higher := newLearner(t, pricedConfig())
		higher.climb(6, 3, 0, 1000000)
		higher.climb(6, 3, 0, 500000)
		if r := higher.learned(sessionWindow); r.Measured || r.Intervals != 0 {
			t.Fatalf("a sustained higher rate kept %+v", r)
		}
	})

	t.Run("legacy calibration rates never qualify", func(t *testing.T) {
		l := newLearner(t, pricedConfig())
		legacy := paidrules.RateSample{Agent: "claude", Family: "sonnet", Window: sessionWindow,
			Movement: 0.3, WeightedTokens: 1e6, Intervals: 9}
		if err := l.s.Update(context.Background(), func(st *State) error {
			st.Samples = append(st.Samples, legacy)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if r := l.learned(sessionWindow); r.Measured || r.Intervals != 0 {
			t.Fatalf("a legacy rate counts: %+v", r)
		}
		if r := paidrules.Learned([]paidrules.RateSample{legacy}, "claude", "sonnet", "", sessionWindow); r.Measured || r.Intervals != 0 {
			t.Fatalf("a legacy rate counts without a fingerprint: %+v", r)
		}
		l.climb(1, 1, 0, 1000000)
		if got := l.state().Samples; len(got) != 0 {
			t.Fatalf("legacy rate kept: %+v", got)
		}
	})
}

func slicesHave(samples []paidrules.RateSample, want paidrules.RateSample) bool {
	for _, s := range samples {
		if reflect.DeepEqual(s, want) {
			return true
		}
	}
	return false
}

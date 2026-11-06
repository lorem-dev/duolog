package paidruns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// A task that settled after the read an admission uses still holds its
// window costs: the read could not show what it used, so its headroom does
// not come back until a read made after its end.
func TestSettledTaskHoldsWindowsUntilALaterRead(t *testing.T) {
	ctx := context.Background()
	clock := ledgerNow
	st := testStore(t, &clock)
	readAt := ledgerNow.Add(-30 * time.Second)
	key := WindowKey{Kind: "five_hour", Reset: ledgerNow.Add(3 * time.Hour)}
	task := func(id string, at time.Time) Admission {
		in := freeTask(id, "claude", "personal", ledgerNow)
		in.Reservation.Estimate.Windows = []WindowCost{{Key: key, Used: 0.84, Cost: 0.05, Reserve: 0.10}}
		in.ReadAt = at
		return in
	}
	// 84 % used, 5 % for the task and a 10 % reserve fit.
	mustAdmit(t, st, task("a", readAt))
	if err := st.Settle(ctx, Settlement{ID: "a", Started: true, CompleteUsage: true, Ended: ledgerNow}); err != nil {
		t.Fatal(err)
	}
	// The same read, or a cached copy of it, does not show a's 5 %.
	mustRefuse(t, st, task("b", readAt), ReasonWindow)
	// A read after a's end shows it in its usage.
	mustAdmit(t, st, task("c", ledgerNow.Add(time.Second)))
}

// largeRates are rates of account fingerprint fp that make Claude's tier
// large, in both account windows, with three intervals each.
func largeRates(fp string) []RateSample {
	return []RateSample{
		{Agent: "claude", Family: "sonnet", Fingerprint: fp, Window: WindowKey{Kind: "session"},
			Movement: 0.15, WeightedTokens: 25e6, Intervals: 3},
		{Agent: "claude", Family: "sonnet", Fingerprint: fp, Window: WindowKey{Kind: "weekly_all"},
			Movement: 0.15, WeightedTokens: 100e6, Intervals: 3},
	}
}

// A read that finds another account drops the old account's rates and tier.
// The admission made from that read uses neither, starts at small and costs
// the task at the cold bound.
func TestGateRatesFollowTheIdentityOfTheRead(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	keyA, err := w.store.Identity("claude", quota.IdentityAccount, []byte("account-a"))
	if err != nil {
		t.Fatal(err)
	}
	shape, _ := w.read(ctx, workerenv.Process{Agent: "claude"})
	if err := w.store.Update(ctx, func(st *State) error {
		st.Samples = largeRates(Fingerprint("claude", keyA, shape))
		st.Tiers["claude"] = TierResult{Tier: TierLarge, Limit: 10}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := w.gate.Cache.Read
	w.gate.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
		// The identity sink of the read records account B.
		if _, err := w.store.Identity("claude", quota.IdentityAccount, []byte("account-b")); err != nil {
			return quota.Snapshot{}, err
		}
		return read(ctx, p)
	}
	p := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 100))
	st := w.state()
	if tier := st.Tiers["claude"]; tier.Tier != TierSmall || tier.Limit != config.DefaultSessionTiers().Small {
		t.Fatalf("tier after an account change = %+v, want small", tier)
	}
	if got, want := p.Reservation.AccountKey, st.AccountKeys["claude/"+quota.IdentityAccount]; got != want || got == keyA {
		t.Fatalf("reservation key %q, want account B's %q", got, want)
	}
	for _, c := range p.Reservation.Estimate.Windows {
		_, bound := windowTerms(quota.Window{DurationMinutes: map[string]int{"session": 300, "weekly_all": 10080}[c.Key.Kind]})
		if !near(c.Cost, bound) {
			t.Fatalf("%s costs %v from the old account's rates, want the bound %v", c.Key.Kind, c.Cost, bound)
		}
	}
}

// An admission whose estimate and tier were made for one account is refused
// when the state holds another by the time it enters, and it writes neither
// a reservation nor its tier.
func TestAdmissionRefusesAnAccountThatChanged(t *testing.T) {
	ctx := context.Background()
	clock := ledgerNow
	st := testStore(t, &clock)
	if _, err := st.Identity("claude", quota.IdentityAccount, []byte("account-b")); err != nil {
		t.Fatal(err)
	}
	old := "key-of-account-a"
	in := freeTask("a", "claude", "personal", ledgerNow)
	in.accountKey = &old
	e, err := st.enter(ctx, in, &line{}, TierResult{Tier: TierLarge, Limit: 10}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if e.Admitted || e.Reason != ReasonAccountChanged {
		t.Fatalf("got %+v, want refused with %q", e.Decision, ReasonAccountChanged)
	}
	got := readState(t, st)
	if len(got.Reservations) != 0 || got.Tiers["claude"].Tier == TierLarge {
		t.Fatalf("a refused admission wrote %+v and tier %+v", got.Reservations, got.Tiers["claude"])
	}
}

// The learner qualifies intervals at balance.min_interval_share and resets a
// drifted window at balance.margin, as tier selection reads them.
func TestRatesUseConfiguredIntervalAndMargin(t *testing.T) {
	t.Run("min_interval_share", func(t *testing.T) {
		cfg := pricedConfig()
		cfg.Balance = config.Balance{MaxAgeSeconds: 120, MinIntervalShare: 0.01, Margin: 0.2}
		l := newLearner(t, cfg)
		l.climb(3, 1, 1, 1_000_000)
		if got := l.learned(sessionWindow); got.Intervals != 3 || !got.Measured {
			t.Fatalf("one-point intervals at min_interval_share 0.01: %+v, want 3 measured", got)
		}
	})
	t.Run("margin", func(t *testing.T) {
		cfg := pricedConfig()
		cfg.Balance = config.Balance{MaxAgeSeconds: 120, MinIntervalShare: 0.05, Margin: 0.5}
		l := newLearner(t, cfg)
		l.climb(3, 5, 1, 1_000_000)
		// 30 % above the earlier rate: within a 50 % margin.
		l.climb(3, 6.5, 1, 1_000_000)
		if got := l.learned(sessionWindow); got.Intervals != 6 {
			t.Fatalf("a 30 %% drift at margin 0.5 reset the window: %+v", got)
		}
	})
}

// A worker's settings layers are found with the environment it runs with,
// not duolog's: an env_passthrough without CLAUDE_CONFIG_DIR leaves the
// worker on the home folder's user settings, so a route name there is a
// signal even when duolog's own CLAUDE_CONFIG_DIR points elsewhere.
func TestGateLayersUseTheWorkersEnvironment(t *testing.T) {
	w := newGateWorld(t)
	home, other := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"),
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	w.cfg.EnvPassthrough = []string{"HOME"}
	w.gate.Layers = ReadLayers
	req := w.request("claude", workerenv.Worker, workerenv.Free, 100)
	environ := []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + other}
	req.Input.Environ = environ
	req.Input.Locations = workerenv.Locations{Home: home, GOOS: "linux", Environ: environ}
	r := w.refused(req, ReasonFreeVariantPaid)
	if !slices.ContainsFunc(r.Signals, func(s workerenv.Signal) bool { return s.Name == "env:ANTHROPIC_BASE_URL" && s.Layer == "user" }) {
		t.Fatalf("signals %+v lack ANTHROPIC_BASE_URL of the user layer", r.Signals)
	}
}

// Pace balancing reads quota in the free worker the person's config builds,
// so a proxy or CA variable in env_passthrough reaches the read.
func TestDefaultPaceBalanceReadsWithThePersonsPassthrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("CORP_TEST_CA_BUNDLE", "/certs/test.pem")
	cfg := gateConfig()
	cfg.Routing.Balance = "pace"
	cfg.EnvPassthrough = append(config.DefaultEnvPassthrough(runtime.GOOS), "CORP_TEST_CA_BUNDLE")
	var envs [][]string
	read := PaceRead
	PaceRead = func(_ context.Context, p workerenv.Process) (quota.Snapshot, error) {
		envs = append(envs, p.Env)
		return quota.Snapshot{}, errors.New("no read in tests")
	}
	t.Cleanup(func() { PaceRead = read })
	DefaultPaceBalance(context.Background(), cfg)
	if len(envs) != len(paceAgents) {
		t.Fatalf("%d pace reads, want %d", len(envs), len(paceAgents))
	}
	for _, env := range envs {
		if !slices.Contains(env, "CORP_TEST_CA_BUNDLE=/certs/test.pem") {
			t.Fatalf("pace read env lacks the listed CA variable: %v", env)
		}
	}
}

// An ordinary worker's run with complete usage feeds the rate learner: the
// movement between its admission's read and a read after it ends, over its
// tokens. A run with incomplete usage, a paid run and a calibration cell,
// which observes its own steps, learn nothing here.
func TestGateLearnsRatesFromOrdinaryWorkers(t *testing.T) {
	// The fake read's resets move with its clock; a gap inside the reset
	// tolerance keeps them one window.
	const learnGap = 30 * time.Second
	sessionSamples := func(w *gateWorld) []RateSample {
		var out []RateSample
		for _, s := range w.state().Samples {
			if s.Agent == "claude" && s.Window.Kind == "session" {
				out = append(out, s)
			}
		}
		return out
	}
	t.Run("complete usage learns", func(t *testing.T) {
		w := newGateWorld(t)
		p := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 100))
		w.now, w.used = w.now.Add(learnGap), 16
		w.finish(p, Settlement{Started: true, CompleteUsage: true, Input: 1_000_000})
		got := sessionSamples(w)
		if len(got) != 1 || got[0].Family != "sonnet" || !near(got[0].Movement, 0.06) || !near(got[0].WeightedTokens, 1e6) {
			t.Fatalf("session samples after an ordinary run: %+v, want one of 6 %% over 1M tokens", got)
		}
	})
	t.Run("incomplete usage learns nothing", func(t *testing.T) {
		w := newGateWorld(t)
		p := w.acquire(w.request("claude", workerenv.Worker, workerenv.Free, 100))
		reads := w.reads
		w.now, w.used = w.now.Add(learnGap), 16
		w.finish(p, Settlement{Started: true, Input: 1_000_000})
		if got := sessionSamples(w); len(got) != 0 || w.reads != reads {
			t.Fatalf("a run with incomplete usage learned %+v with %d reads", got, w.reads-reads)
		}
	})
	t.Run("calibration learns nothing here", func(t *testing.T) {
		w := newGateWorld(t)
		req := w.request("claude", workerenv.Worker, workerenv.Free, 100)
		req.Calibration = true
		p := w.acquire(req)
		w.now, w.used = w.now.Add(learnGap), 16
		w.finish(p, Settlement{Started: true, CompleteUsage: true, Input: 1_000_000})
		if got := sessionSamples(w); len(got) != 0 {
			t.Fatalf("a calibration cell learned %+v in Finish", got)
		}
	})
}

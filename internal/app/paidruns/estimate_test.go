package paidruns

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/usage"
)

func pricedConfig() *config.Config {
	cfg := testConfig()
	cfg.Models = map[string]config.Model{
		"sonnet": {Agent: "claude", PricedFor: "claude-sonnet-5-5", Input: 2, Cached: 0.1, Output: 10},
		"opus":   {Agent: "claude", PricedFor: "claude-opus-5-5", Input: 4, Cached: 0.2, Output: 20},
		"sol":    {Agent: "codex", Family: "sol", PricedFor: "gpt-6.1-sol", Input: 2, Cached: 0.1, Output: 10},
	}
	return cfg
}

// sized is a finished delegation of size n: n*100K input, half of it
// cached, and n*10K output.
func sized(agent, model, role, class string, n int64) journal.Delegation {
	return journal.Delegation{Agent: agent, Model: model, Role: role, Class: class, Done: true,
		Input: n * 100000, Cached: n * 50000, Output: n * 10000}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// A recorded run's Claude cache writes cost 1.25 or 2 times input, so the
// estimate prices them, not the plain input rate.
func TestEstimatePricesCacheWrites(t *testing.T) {
	cfg := pricedConfig()
	d := sized("claude", "claude-sonnet-5-5", "implement", "small", 1)
	d.CacheWrite, d.CacheWriteHour = 40000, 10000
	e, err := EstimateTask(cfg, []journal.Delegation{d}, "claude", "sonnet", "implement", "small")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := usage.Cost(cfg, "claude", "sonnet", usage.Tokens{Input: 100000, Cached: 50000, Write: 40000, WriteHour: 10000, Output: 10000})
	plain, _ := usage.Cost(cfg, "claude", "sonnet", usage.Tokens{Input: 100000, Cached: 50000, Output: 10000})
	if !near(e.USD, want) || want <= plain {
		t.Fatalf("usd = %v, want %v (plain %v)", e.USD, want, plain)
	}
}

func TestEstimateP90ByTargetAndClass(t *testing.T) {
	cfg := pricedConfig()

	// Ten runs of the target and class, sizes 1 to 10; the size 9 run failed
	// on a provider limit after it used its tokens.
	var records []journal.Delegation
	for n := int64(1); n <= 10; n++ {
		d := sized("claude", "claude-sonnet-5-5", "implement", "small", n)
		if n%2 == 0 {
			d.Model = "sonnet"
		}
		if n == 9 {
			d.Done, d.Interrupted, d.Outcome = false, true, journal.OutcomeInfra
		}
		records = append(records, d)
	}
	profile := sized("claude", "claude-sonnet-5-5", "implement", "small", 40)
	profile.ProfileOf = "d-1"
	unused := journal.Delegation{Agent: "claude", Model: "claude-sonnet-5-5", Role: "implement", Class: "small", Outcome: journal.OutcomeLead}
	records = append(records, profile, unused,
		sized("claude", "claude-opus-5-5", "implement", "small", 30),
		sized("codex", "gpt-6.1-sol", "implement", "small", 30),
		sized("claude", "claude-sonnet-5-5", "implement", "large", 30),
	)

	t.Run("class p90 includes failures", func(t *testing.T) {
		e, err := EstimateTask(cfg, records, "claude", "sonnet", "implement", "small")
		if err != nil {
			t.Fatal(err)
		}
		// Nearest rank of 10 runs is the 9th: the failed run. A profile
		// rerun or a run without usage would move it to the 10th.
		if e.Input != 900000 || e.Cached != 450000 || e.Output != 90000 {
			t.Fatalf("tokens = %d/%d/%d, want the size 9 run", e.Input, e.Cached, e.Output)
		}
		want, ok := usage.Cost(cfg, "claude", "sonnet", usage.Tokens{Input: 900000, Cached: 450000, Output: 90000})
		if !ok || !near(e.USD, want) {
			t.Fatalf("USD = %v, want usage.Cost %v", e.USD, want)
		}
		// The cached subset is priced once, at the cached price.
		if !near(e.USD, (450000*2+450000*0.1+90000*10)/1e6) {
			t.Fatalf("USD = %v prices cached input twice", e.USD)
		}
		if !strings.Contains(e.Source, "p90") || !strings.Contains(e.Source, "class small") || !strings.Contains(e.Source, "10 runs") {
			t.Fatalf("Source = %q", e.Source)
		}
		if len(e.Windows) != 0 {
			t.Fatalf("windows = %v; WindowCosts fills them", e.Windows)
		}
	})

	t.Run("role when class is unavailable", func(t *testing.T) {
		byRole := []journal.Delegation{
			sized("claude", "claude-sonnet-5-5", "implement", "small", 2),
			sized("claude", "claude-sonnet-5-5", "implement", "large", 4),
			sized("claude", "claude-sonnet-5-5", "docs", "small", 30),
		}
		for _, class := range []string{"", "unclassified"} {
			e, err := EstimateTask(cfg, byRole, "claude", "sonnet", "implement", class)
			if err != nil {
				t.Fatal(err)
			}
			if e.Input != 400000 || !strings.Contains(e.Source, "role implement") || !strings.Contains(e.Source, "2 runs") {
				t.Fatalf("class %q: input %d, source %q", class, e.Input, e.Source)
			}
		}
	})

	t.Run("empty history uses the bound", func(t *testing.T) {
		for _, history := range [][]journal.Delegation{nil, records} {
			e, err := EstimateTask(cfg, history, "claude", "sonnet", "implement", "medium")
			if err != nil {
				t.Fatal(err)
			}
			if e.Input != 1275000 || e.Cached != 0 || e.Output != 225000 {
				t.Fatalf("tokens = %d/%d/%d, want 85%% of 1.5M input, 15%% output", e.Input, e.Cached, e.Output)
			}
			if !near(e.USD, (1275000*2+225000*10)/1e6) {
				t.Fatalf("USD = %v", e.USD)
			}
			if !strings.Contains(e.Source, "1.5M tokens") || !strings.Contains(e.Source, "85% input") {
				t.Fatalf("Source = %q does not name the bound", e.Source)
			}
		}
	})

	t.Run("missing or invalid prices refuse", func(t *testing.T) {
		broken := map[string]config.Model{
			"missing":     {},
			"other agent": {Agent: "codex", Input: 2, Cached: 0.1, Output: 10},
			"zero":        {Agent: "claude"},
			"negative":    {Agent: "claude", Input: -2, Cached: 0.1, Output: 10},
			"no output":   {Agent: "claude", Input: 2, Cached: 0.1},
			"NaN":         {Agent: "claude", Input: math.NaN(), Cached: 0.1, Output: 10},
			"infinite":    {Agent: "claude", Input: 2, Cached: math.Inf(1), Output: 10},
		}
		for name, price := range broken {
			cfg := pricedConfig()
			if name == "missing" {
				delete(cfg.Models, "sonnet")
			} else {
				cfg.Models["sonnet"] = price
			}
			e, err := EstimateTask(cfg, nil, "claude", "sonnet", "implement", "small")
			if !errors.Is(err, ErrUnpriced) {
				t.Fatalf("%s: err = %v, want ErrUnpriced", name, err)
			}
			// The refusal shows the bound the estimate used; the tokens
			// stay for window math, the money is never a zero estimate.
			if !strings.Contains(err.Error(), "1.5M tokens") || e.Input != 1275000 || e.USD != 0 {
				t.Fatalf("%s: err %q, estimate %+v", name, err, e)
			}
			_, err = EstimateTask(cfg, records, "claude", "sonnet", "implement", "small")
			if !errors.Is(err, ErrUnpriced) {
				t.Fatalf("%s with history: err = %v", name, err)
			}
		}
	})

	t.Run("invalid records and config", func(t *testing.T) {
		bad := []journal.Delegation{
			{Agent: "claude", Model: "sonnet", Class: "small", Input: -1, Output: 10},
			{Agent: "claude", Model: "sonnet", Class: "small", Input: 10, Cached: 11, Output: 10},
			{Agent: "claude", Model: "sonnet", Class: "small", Input: 10, Output: -1},
		}
		for _, d := range bad {
			_, err := EstimateTask(cfg, []journal.Delegation{d}, "claude", "sonnet", "implement", "small")
			if err == nil || errors.Is(err, ErrUnpriced) {
				t.Fatalf("record %+v: err = %v", d, err)
			}
		}
		// A broken record of another target does not refuse this one.
		other := bad[0]
		other.Model = "opus"
		if _, err := EstimateTask(cfg, []journal.Delegation{other}, "claude", "sonnet", "implement", "small"); err != nil {
			t.Fatal(err)
		}
		if _, err := EstimateTask(nil, nil, "claude", "sonnet", "implement", "small"); err == nil {
			t.Fatal("nil config estimated")
		}
	})
}

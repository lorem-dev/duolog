package doctor

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// TestProbeRefusesSettingsRoute is review finding I1: the user's Claude
// settings send calls through a gateway, the login is claude.ai and paid runs
// are off. doctor reports the worker as paid, so the probe, a worker of the
// same shape, refuses and makes no model call.
func TestProbeRefusesSettingsRoute(t *testing.T) {
	cfg, _, a := setup(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://gw.example.com"}}`)
	g := reportGate(t, cfg, nil)
	g.Layers = paidruns.ReadLayers
	err = probe(context.Background(), testDeps, "claude", cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_BASE_URL") {
		t.Fatalf("probe with a gateway in user settings: %v, want a refusal naming it", err)
	}
	if n := len(a.claude.Calls); n != 0 {
		t.Fatalf("%d model calls on a paid route", n)
	}
}

// TestPaidProbePricesCacheWrites is review finding M3: a paid probe that
// reports cache creation tokens and no CLI cost settles them at the write
// rates, 1.25 and 2 times the input price, not at the plain input rate.
func TestPaidProbePricesCacheWrites(t *testing.T) {
	cfg, _, a := setup(t)
	t.Setenv("ANTHROPIC_API_KEY", probeKey)
	cfg.PaidRuns.Claude.Allow, cfg.PaidRuns.Claude.DailyCapUSD = true, 100
	cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
	g := reportGate(t, cfg, paidReads(10))
	a.claude.Usage = Usage{Tokens: usage.Tokens{Input: 2000000, Write: 2000000, WriteHour: 1000000}, Known: true}
	if err := probe(context.Background(), testDeps, "claude", cfg, nil); err != nil {
		t.Fatal(err)
	}
	in := cfg.Models["haiku"].Input
	want := (1e6*in*usage.WriteRate + 1e6*in*usage.WriteHourRate) / 1e6
	st := accountState(t, g)
	if len(st.Settled) != 1 {
		t.Fatalf("settled %+v", st.Settled)
	}
	for _, s := range st.Settled {
		if math.Abs(s.PricedUSD-want) > 1e-9 || s.ChargedUSD < want-1e-9 {
			t.Fatalf("priced %v charged %v, want %v at the write rates", s.PricedUSD, s.ChargedUSD, want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

package doctor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/account"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

const (
	probeKey    = "sk-ant-probe-secret"
	probeMarker = "DUOLOG_TEST_PROBE_PARENT"
)

func accountState(t *testing.T, g *paidruns.Gate) paidruns.State {
	t.Helper()
	var st paidruns.State
	if err := g.Store.Update(context.Background(), func(s *paidruns.State) error { st = *s; return nil }); err != nil {
		t.Fatal(err)
	}
	return st
}

func paidReads(used float64) func(context.Context, corelaunch.Process) (account.Snapshot, error) {
	return func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
		s, err := paidrunstest.Subscription(ctx, p)
		s.Billing.RouteSignals = []corelaunch.Signal{{Name: "auth:apiKey", Layer: "auth status"}}
		s.Windows[0].UsedPercent = used
		return s, err
	}
}

// The probe is a worker run like any other: the free worker's process, its
// environment and its settings, admitted by the gate and settled with what
// it measured. A paid route runs only with paid runs allowed and room under
// the cap; otherwise no model call is made. An allowed probe takes a live
// worker slot and keeps the window reserve. The probe's own config-ignoring
// flags are gone, so its route is the worker's.
func TestProbeUsesGateAndBuild(t *testing.T) {
	ctx := context.Background()

	t.Run("free worker process", func(t *testing.T) {
		cfg, _, a := setup(t)
		t.Setenv("ANTHROPIC_API_KEY", probeKey)
		t.Setenv(probeMarker, "1")
		cfg.EnvPassthrough = append(cfg.EnvPassthrough, "ANTHROPIC_API_KEY")
		g := reportGate(t, cfg, nil)
		if err := probe(ctx, testDeps, "claude", cfg, nil); err != nil {
			t.Fatal(err)
		}
		if len(a.claude.Calls) != 1 {
			t.Fatalf("claude probe calls = %d, want 1", len(a.claude.Calls))
		}
		// The probe gets the process the gate built, not the person's own.
		p := a.claude.Calls[0].Process
		args := p.Argv()
		if args[1] != "--settings" || !strings.Contains(args[2], `"fastMode":false`) || slices.Contains(args, "--setting-sources") {
			t.Fatalf("claude probe argv: %q", args)
		}
		for _, pair := range p.Env {
			if strings.HasPrefix(pair, "ANTHROPIC_API_KEY=") || strings.HasPrefix(pair, probeMarker+"=") {
				t.Fatalf("probe env holds %s", pair)
			}
		}
		if !slices.Contains(p.Env, "DUOLOG_DEPTH=1") {
			t.Fatalf("probe env lacks the worker depth: %q", p.Env)
		}
		entries := []catalogEntry{{Entry: route.Entry{Slug: "gpt-6-luna", Visibility: "list"}}}
		if err := probe(ctx, testDeps, "codex", cfg, entries); err != nil {
			t.Fatal(err)
		}
		if len(a.codex.Calls) != 1 {
			t.Fatalf("codex probe calls = %d, want 1", len(a.codex.Calls))
		}
		line := strings.Join(a.codex.Calls[0].Process.Argv(), " ")
		if !strings.HasPrefix(line, "codex -c check_for_update_on_startup=false") || strings.Count(line, "check_for_update_on_startup") != 1 || strings.Contains(line, "--ignore-user-config") {
			t.Fatalf("codex probe argv: %q", line)
		}
		st := accountState(t, g)
		if len(st.Reservations) != 0 || len(st.Settled) != 2 {
			t.Fatalf("probes not settled once each: %+v %+v", st.Reservations, st.Settled)
		}
		for _, s := range st.Settled {
			if !s.Started || s.ChargedUSD != 0 {
				t.Fatalf("free probe settlement: %+v", s)
			}
		}
	})

	t.Run("paid route refused without allow or above the cap", func(t *testing.T) {
		cfg, _, a := setup(t)
		reportGate(t, cfg, paidReads(10))
		err := probe(ctx, testDeps, "claude", cfg, nil)
		if err == nil || !strings.Contains(err.Error(), "paid") {
			t.Fatalf("paid probe without allow: %v", err)
		}
		cfg.PaidRuns.Claude.Allow, cfg.PaidRuns.Claude.DailyCapUSD = true, 0.000001
		cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
		if err := probe(ctx, testDeps, "claude", cfg, nil); err == nil || !strings.Contains(err.Error(), "daily cap") {
			t.Fatalf("paid probe above the cap: %v", err)
		}
		if n := len(a.claude.Calls); n != 0 {
			t.Fatalf("%d model calls on a refused paid route", n)
		}
	})

	t.Run("allowed paid probe settles usage and cost", func(t *testing.T) {
		cfg, _, a := setup(t)
		t.Setenv("ANTHROPIC_API_KEY", probeKey)
		cfg.PaidRuns.Claude.Allow, cfg.PaidRuns.Claude.DailyCapUSD = true, 100
		cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
		g := reportGate(t, cfg, paidReads(10))
		cost := 0.0123
		a.claude.Usage = Usage{Tokens: usage.Tokens{Input: 120, Cached: 20, Output: 5}, CostUSD: &cost, Known: true}
		if err := probe(ctx, testDeps, "claude", cfg, nil); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(a.claude.Calls[0].Process.Env, "ANTHROPIC_API_KEY="+probeKey) {
			t.Fatal("the paid probe lacks its paid env")
		}
		st := accountState(t, g)
		if len(st.Settled) != 1 || len(st.Reservations) != 0 {
			t.Fatalf("paid probe: %+v %+v", st.Reservations, st.Settled)
		}
		for _, s := range st.Settled {
			if s.Input != 120 || s.Cached != 20 || s.Output != 5 || !s.CompleteUsage || s.ReportedUSD == nil || s.ChargedUSD < 0.0123 {
				t.Fatalf("paid probe settlement: %+v", s)
			}
		}
	})

	t.Run("a probe takes a live slot and keeps the window reserve", func(t *testing.T) {
		cfg, _, a := setup(t)
		cfg.Limits.SessionsByTier.Small = 1
		g := reportGate(t, cfg, nil)
		err := g.Store.Update(ctx, func(st *paidruns.State) error {
			st.Reservations["busy"] = paidrules.Reservation{ID: "busy", Agent: "claude", Shape: corelaunch.Worker,
				Variant: corelaunch.Free, Session: true, Task: true, Created: time.Now()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := probe(ctx, testDeps, "claude", cfg, nil); err == nil || !strings.Contains(err.Error(), "sessions 1 of 1") {
			t.Fatalf("probe with no slot: %v", err)
		}
		cfg.Limits.SessionsByTier.Small = 3
		g.Cache.Read = func(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
			s, err := paidrunstest.Subscription(ctx, p)
			s.Windows[0].UsedPercent = 95
			return s, err
		}
		g.Cache = paidrunstest.Cache(g.Cache.Read)
		if err := probe(ctx, testDeps, "claude", cfg, nil); err == nil || !strings.Contains(err.Error(), "no window headroom") {
			t.Fatalf("probe past the window reserve: %v", err)
		}
		if n := len(a.claude.Calls); n != 0 {
			t.Fatalf("%d model calls on refused probes", n)
		}
	})

	t.Run("a paid route in the user settings refuses the free probe", func(t *testing.T) {
		cfg, _, a := setup(t)
		g := reportGate(t, cfg, nil)
		g.Layers = paidruns.ReadLayers
		settings := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings, []byte(`{"apiKeyHelper":"/bin/echo"}`), 0600); err != nil {
			t.Fatal(err)
		}
		err := probe(ctx, testDeps, "claude", cfg, nil)
		if err == nil || !strings.Contains(err.Error(), "apiKeyHelper in user") {
			t.Fatalf("probe over a paid user layer: %v", err)
		}
		if n := len(a.claude.Calls); n != 0 {
			t.Fatalf("%d model calls on a refused probe", n)
		}
	})
}

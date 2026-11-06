package doctor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const (
	probeKey    = "sk-ant-probe-secret"
	probeMarker = "DUOLOG_TEST_PROBE_PARENT"
)

func recordedArgs(t *testing.T, bin string) []string {
	t.Helper()
	var args []string
	data, err := os.ReadFile(bin + ".args.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	return args
}

func recordedEnv(t *testing.T, bin string) []string {
	t.Helper()
	var env []string
	data, err := os.ReadFile(bin + ".env.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

// modelCalls counts the calls of the fake that were model calls.
func modelCalls(t *testing.T, bin string) int {
	t.Helper()
	data, err := os.ReadFile(bin + ".calls.ndjson")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, `"-p"`) || strings.Contains(line, `"exec"`) {
			n++
		}
	}
	return n
}

func accountState(t *testing.T, g *paidruns.Gate) paidruns.State {
	t.Helper()
	var st paidruns.State
	if err := g.Store.Update(context.Background(), func(s *paidruns.State) error { st = *s; return nil }); err != nil {
		t.Fatal(err)
	}
	return st
}

func paidReads(used float64) func(context.Context, workerenv.Process) (quota.Snapshot, error) {
	return func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
		s, err := paidrunstest.Subscription(ctx, p)
		s.Billing.RouteSignals = []workerenv.Signal{{Name: "auth:apiKey", Layer: "auth status"}}
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
		cfg, _, codex := setup(t)
		t.Setenv("ANTHROPIC_API_KEY", probeKey)
		t.Setenv(probeMarker, "1")
		cfg.EnvPassthrough = append(cfg.EnvPassthrough, "ANTHROPIC_API_KEY")
		g := reportGate(t, cfg, nil)
		claude, err := exec.LookPath("claude")
		if err != nil {
			t.Fatal(err)
		}
		if err := probe(ctx, "claude", cfg, nil); err != nil {
			t.Fatal(err)
		}
		args := recordedArgs(t, claude)
		if args[0] != "--settings" || !strings.Contains(args[1], `"fastMode":false`) || slices.Contains(args, "--setting-sources") {
			t.Fatalf("claude probe argv: %q", args)
		}
		env := recordedEnv(t, claude)
		for _, pair := range env {
			if strings.HasPrefix(pair, "ANTHROPIC_API_KEY=") || strings.HasPrefix(pair, probeMarker+"=") {
				t.Fatalf("probe env holds %s", pair)
			}
		}
		if !slices.Contains(env, "DUOLOG_DEPTH=1") {
			t.Fatalf("probe env lacks the worker depth: %q", env)
		}
		entries := []catalogEntry{{Entry: route.Entry{Slug: "gpt-6-luna", Visibility: "list"}}}
		if err := probe(ctx, "codex", cfg, entries); err != nil {
			t.Fatal(err)
		}
		args = recordedArgs(t, codex)
		line := strings.Join(args, " ")
		if !strings.HasPrefix(line, "-c check_for_update_on_startup=false exec ") || strings.Count(line, "check_for_update_on_startup") != 1 || strings.Contains(line, "--ignore-user-config") {
			t.Fatalf("codex probe argv: %q", args)
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
		cfg, _, _ := setup(t)
		reportGate(t, cfg, paidReads(10))
		claude, _ := exec.LookPath("claude")
		err := probe(ctx, "claude", cfg, nil)
		if err == nil || !strings.Contains(err.Error(), "paid") {
			t.Fatalf("paid probe without allow: %v", err)
		}
		cfg.PaidRuns.Claude.Allow, cfg.PaidRuns.Claude.DailyCapUSD = true, 0.000001
		cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
		if err := probe(ctx, "claude", cfg, nil); err == nil || !strings.Contains(err.Error(), "daily cap") {
			t.Fatalf("paid probe above the cap: %v", err)
		}
		if n := modelCalls(t, claude); n != 0 {
			t.Fatalf("%d model calls on a refused paid route", n)
		}
	})

	t.Run("allowed paid probe settles usage and cost", func(t *testing.T) {
		cfg, _, _ := setup(t)
		t.Setenv("ANTHROPIC_API_KEY", probeKey)
		cfg.PaidRuns.Claude.Allow, cfg.PaidRuns.Claude.DailyCapUSD = true, 100
		cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
		g := reportGate(t, cfg, paidReads(10))
		claude := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"is_error":false,"result":"ok","usage":{"input_tokens":100,"cache_read_input_tokens":20,"output_tokens":5},"total_cost_usd":0.0123}`})
		if err := probe(ctx, "claude", cfg, nil); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(recordedEnv(t, claude), "ANTHROPIC_API_KEY="+probeKey) {
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
		cfg, _, _ := setup(t)
		cfg.Limits.SessionsByTier.Small = 1
		g := reportGate(t, cfg, nil)
		claude, _ := exec.LookPath("claude")
		err := g.Store.Update(ctx, func(st *paidruns.State) error {
			st.Reservations["busy"] = paidruns.Reservation{ID: "busy", Agent: "claude", Shape: workerenv.Worker,
				Variant: workerenv.Free, Session: true, Task: true, Created: time.Now()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := probe(ctx, "claude", cfg, nil); err == nil || !strings.Contains(err.Error(), "sessions 1 of 1") {
			t.Fatalf("probe with no slot: %v", err)
		}
		cfg.Limits.SessionsByTier.Small = 3
		g.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
			s, err := paidrunstest.Subscription(ctx, p)
			s.Windows[0].UsedPercent = 95
			return s, err
		}
		g.Cache = &paidruns.Cache{Read: g.Cache.Read}
		if err := probe(ctx, "claude", cfg, nil); err == nil || !strings.Contains(err.Error(), "no window headroom") {
			t.Fatalf("probe past the window reserve: %v", err)
		}
		if n := modelCalls(t, claude); n != 0 {
			t.Fatalf("%d model calls on refused probes", n)
		}
	})

	t.Run("a paid route in the user settings refuses the free probe", func(t *testing.T) {
		cfg, _, _ := setup(t)
		g := reportGate(t, cfg, nil)
		g.Layers = paidruns.ReadLayers
		settings := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings, []byte(`{"apiKeyHelper":"/bin/echo"}`), 0600); err != nil {
			t.Fatal(err)
		}
		claude, _ := exec.LookPath("claude")
		err := probe(ctx, "claude", cfg, nil)
		if err == nil || !strings.Contains(err.Error(), "apiKeyHelper in user") {
			t.Fatalf("probe over a paid user layer: %v", err)
		}
		if n := modelCalls(t, claude); n != 0 {
			t.Fatalf("%d model calls on a refused probe", n)
		}
	})
}

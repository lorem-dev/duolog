package paidstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// The plan a first read finds reaches the process the cache returns, without
// a second read, and the next build starts with it.
func TestCacheGivesTheWorkerTheCompactWindowOfThePlanRead(t *testing.T) {
	cfg := &config.Config{EnvPassthrough: []string{"HOME"}}
	cfg.Workers = config.Workers{CompactWindow: "auto", CompactLadder: map[string]int{"pro": 250000, "max": 500000}}
	reads := 0
	cache := &Cache{
		Now: time.Now,
		Read: func(context.Context, launch.Process) (account.Snapshot, error) {
			reads++
			return account.Snapshot{ReadAt: time.Now(), Plan: "max"}, nil
		},
	}
	build := func() (launch.Process, error) {
		return workerenv.Build(launch.ProcessInput{Agent: "claude", Shape: launch.Worker, Variant: launch.Free,
			Config: cfg, Plan: cache.Plan("claude"), Environ: []string{"HOME=/h"}, Locations: launch.Locations{GOOS: "linux"}})
	}
	p, _, _, err := cache.Get(context.Background(), build, cfg, account.RunEvidence{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Settings, `"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"500000"`) {
		t.Fatalf("settings %s", p.Settings)
	}
	if reads != 1 || cache.Plan("claude") != "max" {
		t.Fatalf("%d reads, plan %q", reads, cache.Plan("claude"))
	}
}

// A read that fails, or that names no plan, leaves the worker or peer on the
// bounded fallback, the smallest ladder value, and never on Claude's own
// default.
func TestCacheGivesTheFallbackWindowWhenTheReadNamesNoPlan(t *testing.T) {
	cfg := &config.Config{EnvPassthrough: []string{"HOME"}}
	cfg.Workers = config.Workers{CompactWindow: "auto", CompactLadder: map[string]int{"pro": 250000, "max": 500000}}
	for name, read := range map[string]func(context.Context, launch.Process) (account.Snapshot, error){
		"failed": func(context.Context, launch.Process) (account.Snapshot, error) {
			return account.Snapshot{}, errors.New("usage read failed; check login")
		},
		"no plan": func(context.Context, launch.Process) (account.Snapshot, error) {
			return account.Snapshot{ReadAt: time.Now()}, nil
		},
	} {
		for _, shape := range []launch.Shape{launch.Worker, launch.Peer} {
			cache := &Cache{Now: time.Now, Read: read}
			build := func() (launch.Process, error) {
				return workerenv.Build(launch.ProcessInput{Agent: "claude", Shape: shape, Variant: launch.Free, PeerID: "p1",
					Config: cfg, Plan: cache.Plan("claude"), Environ: []string{"HOME=/h"}, Locations: launch.Locations{GOOS: "linux"}})
			}
			p, _, _, _ := cache.Get(context.Background(), build, cfg, account.RunEvidence{}, false)
			if !strings.Contains(p.Settings, `"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"250000"`) {
				t.Fatalf("%s read, %s: settings %s", name, shape, p.Settings)
			}
		}
	}
}

// A Get that built its process without a plan rebuilds it, even when another
// Get stored the plan after that build and before this Get compared.
func TestCacheRebuildsWhenAnotherGetStoredThePlan(t *testing.T) {
	cfg := &config.Config{EnvPassthrough: []string{"HOME"}}
	cfg.Workers = config.Workers{CompactWindow: "auto", CompactLadder: map[string]int{"pro": 250000}}
	cache := &Cache{
		Now: time.Now,
		Read: func(context.Context, launch.Process) (account.Snapshot, error) {
			return account.Snapshot{ReadAt: time.Now(), Plan: "pro"}, nil
		},
	}
	builds := 0
	build := func() (launch.Process, error) {
		builds++
		p, err := workerenv.Build(launch.ProcessInput{Agent: "claude", Shape: launch.Worker, Variant: launch.Free,
			Config: cfg, Plan: cache.Plan("claude"), Environ: []string{"HOME=/h"}, Locations: launch.Locations{GOOS: "linux"}})
		if builds == 1 {
			// Another Get stores the plan right after this build.
			cache.mu.Lock()
			cache.plans = map[string]string{"claude": "pro"}
			cache.mu.Unlock()
		}
		return p, err
	}
	p, _, _, err := cache.Get(context.Background(), build, cfg, account.RunEvidence{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Settings, `"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"250000"`) {
		t.Fatalf("built %d times, settings %s", builds, p.Settings)
	}
}

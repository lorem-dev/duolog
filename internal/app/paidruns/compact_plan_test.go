package paidruns

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
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
		Read: func(context.Context, workerenv.Process) (quota.Snapshot, error) {
			reads++
			return quota.Snapshot{ReadAt: time.Now(), Plan: "max"}, nil
		},
	}
	build := func() (workerenv.Process, error) {
		return workerenv.Build(workerenv.ProcessInput{Agent: "claude", Shape: workerenv.Worker, Variant: workerenv.Free,
			Config: cfg, Plan: cache.Plan("claude"), Environ: []string{"HOME=/h"}, Locations: workerenv.Locations{GOOS: "linux"}})
	}
	p, _, _, err := cache.Get(context.Background(), build, cfg, quota.RunEvidence{}, false)
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

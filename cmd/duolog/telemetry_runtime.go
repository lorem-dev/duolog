package main

import (
	"encoding/json"
	"maps"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/record"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/lorem-dev/duolog/internal/usage"
)

// The bounds telemetry checks numbers against: maxSafe for counts, maxSecs
// for durations, maxAttempt for attempt numbers.
const (
	maxSafe    = 1<<53 - 1
	maxSecs    = 1<<31 - 1
	maxAttempt = 1_000_000
)

// telemetryLocation is where this platform keeps telemetry, from the
// environment the store itself never reads.
func telemetryLocation() telemetry.Location {
	home, _ := os.UserHomeDir()
	return telemetry.Location{GOOS: runtime.GOOS, Home: home, XDGStateHome: os.Getenv("XDG_STATE_HOME"), LocalAppData: os.Getenv("LOCALAPPDATA")}
}

// telemetryOptions are the store settings for the project at root, from the
// loaded config: its role and class names, and its StateRoot with symlinks
// resolved, so every worktree of a checkout shares one partition.
func telemetryOptions(cfg *config.Config, root string) telemetry.Options {
	o := telemetry.Options{
		Location:        telemetryLocation(),
		Names:           telemetry.Names{Roles: slices.Sorted(maps.Keys(cfg.Roles)), Classes: slices.Sorted(maps.Keys(cfg.Learning.Classes))},
		Enabled:         cfg.Telemetry.Enabled,
		ProjectEnabled:  cfg.Telemetry.ProjectEnabled,
		RetentionDays:   cfg.Telemetry.RetentionDays,
		PruneEveryHours: cfg.Telemetry.PruneEveryHours,
		MaxMegabytes:    cfg.Telemetry.MaxMegabytes,
	}
	if state, err := filepath.EvalSymlinks(project.StateRoot(root)); err == nil {
		o.CanonicalStateRoot = state
	}
	return o
}

// openTelemetry opens the store for this project's run events. It returns nil
// when they are off or the store cannot be used: a delegation never fails or
// changes on telemetry.
func openTelemetry(cfg *config.Config, root string) *telemetry.Store {
	if !cfg.Telemetry.ProjectEnabled {
		return nil
	}
	s, err := telemetry.Open(telemetryOptions(cfg, root))
	if err != nil {
		return nil
	}
	return s
}

// observeAttempt writes each attempt as a run event. The attempts of one
// delegation share the run id the delegation drew and gave its workers as
// DUOLOG_RUN, so they join the workers' gate events; an attempt without one,
// such as a run here task, gets an id drawn at its first attempt. A vendor id
// is never used or hashed for it. An append that fails is counted in the
// store's health and otherwise ignored.
func observeAttempt(s *telemetry.Store, cfg *config.Config) record.Observer {
	if s == nil {
		return nil
	}
	var run telemetry.ID
	return func(a record.Attempt) {
		if id := runID(a.Run); id != (telemetry.ID{}) {
			run = id
		} else if a.Number <= 1 || run == (telemetry.ID{}) {
			// Without an id the event is still written, with none.
			run, _ = telemetry.NewID()
		}
		_ = s.Append(runEvent(cfg, run, a))
	}
}

// runEvent maps an attempt to its run event with every number in range: usage
// only when the vendor reported it, cached at most the input, and the cost
// from usage.Cost, which charges cache reads and writes once, only when
// priced, finite and in range. Strings pass as they are: the store writes anything outside a
// closed set or the configured names as other.
func runEvent(cfg *config.Config, run telemetry.ID, a record.Attempt) telemetry.Run {
	family := models.Normalize(a.Agent, a.Family)
	e := telemetry.Run{
		Common: telemetry.Common{Run: run},
		Role:   a.Role, Class: a.Class, Agent: a.Agent, Family: family, Effort: a.Effort, Placement: a.Placement,
		Attempt: min(max(a.Number, 1), maxAttempt),
		Secs:    min(max(int64(a.Duration.Round(time.Second)/time.Second), 0), maxSecs),
		Outcome: a.Outcome, Err: a.ErrorCode, Reason: a.Reason,
		Asks: count(a.Asks), Denials: count(a.Denials),
		Escalated: a.Escalated, Moved: a.Moved,
	}
	if !a.UsageKnown {
		return e
	}
	e.UsageKnown = true
	e.In, e.Out = count(a.Usage.Input), count(a.Usage.Output)
	e.Cached = min(count(a.Usage.Cached), e.In)
	// Cache writes are not in the event, but their premium is in the cost.
	write := min(count(a.Usage.Write), e.In-e.Cached)
	t := usage.Tokens{Input: e.In, Cached: e.Cached, Write: write, WriteHour: min(count(a.Usage.WriteHour), write), Output: e.Out}
	if usd, ok := usage.Cost(cfg, a.Agent, family, t); ok {
		// The comparisons also refuse NaN.
		if micro := math.Round(usd * 1e6); micro >= 0 && micro <= maxSafe {
			e.USDmicro, e.Priced = int64(micro), true
		}
	}
	return e
}

func count(n int64) int64 { return min(max(n, 0), maxSafe) }

// runID reads a duolog run id; anything else, a vendor id included, is none.
func runID(value string) telemetry.ID {
	var id telemetry.ID
	data, err := json.Marshal(value)
	if err != nil || json.Unmarshal(data, &id) != nil {
		return telemetry.ID{}
	}
	return id
}

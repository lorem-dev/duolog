package paidruns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// paceSnapshot is a fresh subscription read whose week is 44% elapsed and
// used at week, with a five-hour window used at five.
func paceSnapshot(agent string, week, five float64) quota.Snapshot {
	s := snapshot(agent)
	s.ReadAt = now
	s.Windows = []quota.Window{
		{Kind: "session", DurationMinutes: 300, UsedPercent: five * 100, ResetsAt: now.Add(time.Hour)},
		{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: week * 100,
			ResetsAt: now.Add(338688 * time.Second)}, // 56% of the week left
		// Windows of one model and other kinds never set the pace.
		{Kind: "weekly_opus", Model: "opus", DurationMinutes: 10080, UsedPercent: 99, ResetsAt: now.Add(time.Hour)},
	}
	return s
}

func TestAgentPaceFromFreshSubscriptionRead(t *testing.T) {
	cfg := testConfig()
	for _, agent := range []string{"claude", "codex"} {
		got := paidrules.AgentPace(process(agent, workerenv.Worker, workerenv.Free), paceSnapshot(agent, .61, .2), nil, cfg, now)
		if !got.Subscription || got.Week == nil || got.FiveHour == nil {
			t.Fatalf("%s: pace = %+v, want a subscription with both windows", agent, got)
		}
		if !near(got.Week.Used, .61) || !near(got.Week.Elapsed, .44) || !near(got.FiveHour.Used, .2) {
			t.Errorf("%s: week %+v, five hours %+v; want 0.61 at 0.44 and 0.2", agent, got.Week, got.FiveHour)
		}
	}
}

func TestAgentPaceUnknownQuota(t *testing.T) {
	cfg := testConfig()
	p := process("codex", workerenv.Worker, workerenv.Free)
	good := paceSnapshot("codex", .61, .2)
	stale := good
	stale.ReadAt = now.Add(-time.Hour)
	reset := good
	reset.Windows = []quota.Window{{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 61, ResetsAt: now.Add(-time.Minute)}}
	none := good
	none.Windows = nil
	for name, c := range map[string]struct {
		s   quota.Snapshot
		err error
	}{
		"read failed":      {good, errors.New("boom")},
		"stale read":       {stale, nil},
		"window has reset": {reset, nil},
		"no windows":       {none, nil},
	} {
		got := paidrules.AgentPace(p, c.s, c.err, cfg, now)
		if got.Subscription || got.Week != nil {
			t.Errorf("%s: pace = %+v, want no balancing input", name, got)
		}
	}
}

func TestAgentPaceNeverSubscriptionOnOverageOrPaid(t *testing.T) {
	cfg := testConfig()
	over := paceSnapshot("codex", .1, .1)
	over.Billing.OverageEnabled = true
	if got := paidrules.AgentPace(process("codex", workerenv.Worker, workerenv.Free), over, nil, cfg, now); got.Subscription {
		t.Errorf("overage read is a subscription: %+v", got)
	}
	paid := paceSnapshot("codex", .1, .1)
	if got := paidrules.AgentPace(process("codex", workerenv.Worker, workerenv.Free,
		workerenv.Signal{Name: "env:OPENAI_API_KEY", Layer: "env"}), paid, nil, cfg, now); got.Subscription {
		t.Errorf("paid route is a subscription: %+v", got)
	}
	apiKey := paceSnapshot("claude", .1, .1)
	apiKey.Billing.RouteSignals = []workerenv.Signal{{Name: "auth:api_key", Layer: "auth status"}}
	if got := paidrules.AgentPace(process("claude", workerenv.Worker, workerenv.Free), apiKey, nil, cfg, now); got.Subscription {
		t.Errorf("api key read is a subscription: %+v", got)
	}
}

func TestAgentPaceScalesByWorkspaceShare(t *testing.T) {
	cfg := testConfig()
	pa := cfg.PaidRuns.Agent("codex")
	pa.Account = "workspace"
	cfg.PaidRuns.Set("codex", pa)
	got := paidrules.AgentPace(process("codex", workerenv.Worker, workerenv.Free), paceSnapshot("codex", .30, .1), nil, cfg, now)
	if got.Week == nil || !near(got.Week.Used, .6) {
		t.Errorf("workspace week = %+v, want 0.6 of the usable half", got.Week)
	}
	// The scaled share never passes 1.
	got = paidrules.AgentPace(process("codex", workerenv.Worker, workerenv.Free), paceSnapshot("codex", .80, .1), nil, cfg, now)
	if got.Week == nil || got.Week.Used != 1 {
		t.Errorf("workspace week = %+v, want it capped at 1", got.Week)
	}
}

func TestPaceBalanceReadsBothAgentsAndKeepsState(t *testing.T) {
	cfg := testConfig()
	cfg.Routing = config.Routing{Balance: "pace", Margin: .10}
	dir := t.TempDir()
	SavePaceState(dir, map[string]bool{"codex": true})
	read := func(_ context.Context, p workerenv.Process) (quota.Snapshot, error) {
		return paceSnapshot(p.Agent, map[string]float64{"codex": .61, "claude": .30}[p.Agent], .1), nil
	}
	build := func(agent string) (workerenv.Process, error) {
		return process(agent, workerenv.Worker, workerenv.Free), nil
	}
	b := PaceBalance(context.Background(), cfg, read, build, dir, now)
	if b == nil || b.Mode != "pace" || b.Margin != .10 || !b.Was["codex"] {
		t.Fatalf("balance = %+v", b)
	}
	if !b.Pace["codex"].Subscription || !b.Pace["claude"].Subscription {
		t.Errorf("pace = %+v, want both subscription", b.Pace)
	}

	// A failed build or read leaves that agent unknown, not the other.
	failing := func(_ context.Context, p workerenv.Process) (quota.Snapshot, error) {
		if p.Agent == "codex" {
			return quota.Snapshot{}, errors.New("boom")
		}
		return read(context.Background(), p)
	}
	b = PaceBalance(context.Background(), cfg, failing, build, dir, now)
	if b.Pace["codex"].Subscription || !b.Pace["claude"].Subscription {
		t.Errorf("pace after a failed read = %+v", b.Pace)
	}

	// A switched-off agent is neither built nor read, and has no pace.
	on, off := true, false
	cfg.Agents = map[string]config.AgentSwitch{"claude": {Enabled: &on}, "codex": {Enabled: &off}}
	var touched []string
	b = PaceBalance(context.Background(), cfg, func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
		touched = append(touched, "read "+p.Agent)
		return read(ctx, p)
	}, func(agent string) (workerenv.Process, error) {
		touched = append(touched, "build "+agent)
		return build(agent)
	}, dir, now)
	if _, ok := b.Pace["codex"]; ok || !b.Pace["claude"].Subscription || !slices.Equal(touched, []string{"build claude", "read claude"}) {
		t.Errorf("pace with codex off = %+v after %q, want only claude built and read", b.Pace, touched)
	}
	cfg.Agents = nil

	// Off reads nothing.
	cfg.Routing.Balance = "off"
	if b := PaceBalance(context.Background(), cfg, func(context.Context, workerenv.Process) (quota.Snapshot, error) {
		t.Error("balance off read the quota")
		return quota.Snapshot{}, nil
	}, build, dir, now); b != nil {
		t.Errorf("off balance = %+v, want nil", b)
	}
}

// TestPaceReadDoesNotRetry: a pace read that times out is not read again,
// as the account gate's read is; the agent is only left out of the balance,
// so a loaded machine waits one budget per agent on the way to a route.
func TestPaceReadDoesNotRetry(t *testing.T) {
	defer func(r quota.Reader) { paceReader = r }(paceReader)
	paceReader.Timeout = time.Second
	identityHomes(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{SleepMS: 60000})
	cfg := testConfig()
	cfg.Routing = config.Routing{Balance: "pace"}
	build := func(agent string) (workerenv.Process, error) {
		if agent != "claude" {
			return workerenv.Process{}, errors.New("claude only")
		}
		return freeWorker(agent)()
	}
	b := PaceBalance(context.Background(), cfg, PaceRead, build, t.TempDir(), time.Now())
	if b == nil || b.Pace["claude"].Subscription {
		t.Fatalf("balance after a timed-out read = %+v", b)
	}
	calls, err := os.ReadFile(claude + ".calls.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(string(calls)), "\n") + 1; n != 1 {
		t.Fatalf("%d claude starts for one pace read, want 1:\n%s", n, calls)
	}
}

func TestPaceStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := loadPaceState(dir); len(got) != 0 {
		t.Errorf("missing state = %v", got)
	}
	SavePaceState(dir, map[string]bool{"codex": true})
	SavePaceState(dir, map[string]bool{"claude": false})
	got := loadPaceState(dir)
	if !got["codex"] || got["claude"] || len(got) != 2 {
		t.Errorf("state = %v, want codex true, claude false", got)
	}
	SavePaceState(dir, map[string]bool{"codex": false})
	if got := loadPaceState(dir); got["codex"] {
		t.Errorf("codex still ahead: %v", got)
	}
	// Unreadable state is no state.
	if err := os.WriteFile(filepath.Join(dir, paceStateFile), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadPaceState(dir); len(got) != 0 {
		t.Errorf("corrupt state = %v", got)
	}
	// Saving nothing, or into no folder, writes nothing.
	SavePaceState(dir, nil)
	SavePaceState("", map[string]bool{"codex": true})
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("folder holds %d entries after empty saves, want only the corrupt file", len(entries))
	}
}

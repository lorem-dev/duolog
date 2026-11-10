package paidstore

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// Kills probing on every new cache instance, or renewing the retained read's age.
func TestClaudeProbeReusesRetainedRead(t *testing.T) {
	bin := installWeeklyClaude(t, testbin.Scenario{Stdout: weeklyClaudeAuth})
	dir := t.TempDir()
	cfg := &config.Config{Balance: config.Balance{MaxAgeSeconds: 60}}
	p := launch.Process{Agent: "claude", Shape: launch.Worker, Variant: launch.Free, Prefix: []string{bin}, Env: []string{}, CacheKey: "weekly-route"}
	build := func() (launch.Process, error) { return p, nil }
	r := quota.Reader{Once: true, Timeout: 3 * time.Second}
	var first account.Snapshot
	for i, age := range []time.Duration{0, 30 * time.Second, 61 * time.Second} {
		now := time.Now()
		if i > 0 {
			now = first.ReadAt.Add(age)
		}
		c := &Cache{Read: r.ReadProcess, Dir: dir, Now: func() time.Time { return now }}
		_, s, a, err := c.Get(context.Background(), build, cfg, account.RunEvidence{}, false)
		if err != nil || len(s.Windows) != 1 || i < 2 && a.Verdict != paidrules.SubscriptionOverage {
			t.Fatalf("read %d: %+v %v", i, a, err)
		}
		if i == 0 {
			first = s
		}
		want := []int{1, 1, 2}[i]
		if got := probeCalls(t, bin); got != want {
			t.Fatalf("read %d: %d probes, want %d", i, got, want)
		}
		if i == 1 && (!s.Recalled || !s.ReadAt.Equal(first.ReadAt) || s.Source != first.Source) {
			t.Fatalf("retained read was renewed or lost its source: %+v, first %+v", s, first)
		}
	}
}

// Kills using another route's retained windows to suppress a needed probe.
func TestClaudeProbeDoesNotReuseAnotherRoute(t *testing.T) {
	bin := installWeeklyClaude(t, testbin.Scenario{Stdout: weeklyClaudeAuth})
	dir := t.TempDir()
	r := quota.Reader{Once: true, Timeout: 3 * time.Second}
	for _, key := range []string{"first-route", "second-route"} {
		p := launch.Process{Agent: "claude", Shape: launch.Worker, Variant: launch.Free, Prefix: []string{bin}, Env: []string{}, CacheKey: key}
		c := &Cache{Read: r.ReadProcess, Dir: dir}
		_, s, _, err := c.Get(context.Background(), func() (launch.Process, error) { return p, nil }, &config.Config{}, account.RunEvidence{}, false)
		if err != nil || s.Recalled {
			t.Fatalf("route %s: %+v %v", key, s, err)
		}
	}
	if got := probeCalls(t, bin); got != 2 {
		t.Fatalf("%d probes, want 2", got)
	}
}

// Kills letting retained subscription evidence hide a current API-key login.
func TestClaudeProbeReuseChecksCurrentAuth(t *testing.T) {
	bin := installWeeklyClaude(t, testbin.Scenario{Sequence: []testbin.Scenario{
		{Stdout: weeklyClaudeAuth}, {Stdout: `{"loggedIn":true,"authMethod":"api_key","apiProvider":"firstParty","subscriptionType":"max"}`},
	}})
	dir := t.TempDir()
	r := quota.Reader{Once: true, Timeout: 3 * time.Second}
	p := launch.Process{Agent: "claude", Shape: launch.Worker, Variant: launch.Free, Prefix: []string{bin}, Env: []string{}, CacheKey: "weekly-route"}
	for i := range 2 {
		c := &Cache{Read: r.ReadProcess, Dir: dir}
		_, s, a, err := c.Get(context.Background(), func() (launch.Process, error) { return p, nil }, &config.Config{}, account.RunEvidence{}, false)
		if i == 0 && err != nil || i == 1 && (err == nil || s.Recalled || a.Verdict != paidrules.Paid) {
			t.Fatalf("read %d: %+v %+v %v", i, s, a, err)
		}
	}
	if got := probeCalls(t, bin); got != 1 {
		t.Fatalf("%d probes, want 1", got)
	}
}

// Kills probing on forced or post-run reads while retained windows are fresh,
// or treating retained windows as a new post-run rate measurement.
func TestClaudeProbeReuseOnExplicitReads(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh admission", true: "after run"}[after], func(t *testing.T) {
			bin := installWeeklyClaude(t, testbin.Scenario{Stdout: weeklyClaudeAuth})
			dir := t.TempDir()
			r := quota.Reader{Once: true, Timeout: 3 * time.Second}
			p := launch.Process{Agent: "claude", Shape: launch.Worker, Variant: launch.Free, Prefix: []string{bin}, Env: []string{}, CacheKey: "weekly-route"}
			build := func() (launch.Process, error) { return p, nil }
			cfg := &config.Config{}
			c := &Cache{Read: r.ReadProcess, Dir: dir}
			_, first, _, err := c.Get(context.Background(), build, cfg, account.RunEvidence{}, false)
			if err != nil {
				t.Fatal(err)
			}
			c = &Cache{Read: r.ReadProcess, Dir: dir}
			var s account.Snapshot
			if after {
				var fresh bool
				s, fresh, err = c.After(context.Background(), p, cfg)
				if !fresh {
					t.Error("retained windows became a new measurement")
				}
			} else {
				_, s, _, err = c.Get(context.Background(), build, cfg, account.RunEvidence{}, true)
			}
			if err != nil || !s.Recalled || !s.ReadAt.Equal(first.ReadAt) || probeCalls(t, bin) != 1 {
				t.Fatalf("read %+v err %v probes %d", s, err, probeCalls(t, bin))
			}
		})
	}
}

const weeklyClaudeAuth = `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","subscriptionType":"max"}`

func installWeeklyClaude(t *testing.T, auth testbin.Scenario) string {
	t.Helper()
	const event = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1794744000,"rateLimitType":"seven_day","overageStatus":"allowed","overageResetsAt":1796083200,"isUsingOverage":false,"unifiedWindows":{"seven_day":{"utilization":0.09,"resetsAt":1794744000,"status":"allowed"}}}}`
	return testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"-p /usage":          {Stdout: `{"type":"result","result":"What's contributing to your usage?"}` + "\n"},
		"auth status --json": auth,
		"-p ok":              {Stdout: event + "\n"},
	}})
}

func probeCalls(t *testing.T, bin string) int {
	t.Helper()
	data, err := os.ReadFile(bin + ".calls.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		if len(args) > 1 && args[0] == "-p" && args[1] == "ok" {
			n++
		}
	}
	return n
}

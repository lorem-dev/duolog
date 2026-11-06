package paidruns

import (
	"math"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// The helpers below are those of the rules' tests in core/paidrules that the
// tests here use too.

const secretValue = "sk-test-secret-value"

var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func yes() *bool { v := true; return &v }

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.PaidRuns.UsableShare = 0.5
	for _, a := range []*config.PaidAgent{&cfg.PaidRuns.Claude.PaidAgent, &cfg.PaidRuns.Codex} {
		a.Account = "auto"
	}
	return cfg
}

func windows() []account.Window {
	return []account.Window{
		{Kind: "session", DurationMinutes: 300, UsedPercent: 12, ResetsAt: now.Add(2 * time.Hour)},
		{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 30, ResetsAt: now.Add(72 * time.Hour)},
	}
}

// snapshot is a successful first-party read with future windows and no
// overage.
func snapshot(agent string) account.Snapshot {
	s := account.Snapshot{Plan: "max", Windows: windows(), ReadAt: now.Add(-10 * time.Second)}
	switch agent {
	case "claude":
		s.Billing = account.BillingFacts{AuthMethod: "claude.ai", APIProvider: "firstParty", RouteKnown: true, WindowsAvailable: yes()}
	case "codex":
		s.Plan = "plus"
		s.Billing = account.BillingFacts{AccountType: "chatgpt", AuthMode: "chatgpt", RouteKnown: true, OrdinaryUsageAllowed: yes()}
	}
	return s
}

func process(agent string, shape launch.Shape, variant launch.Variant, signals ...launch.Signal) launch.Process {
	return launch.Process{Agent: agent, Shape: shape, Variant: variant, Signals: signals,
		Env: []string{"SOME_KEY=" + secretValue}, Settings: `{"env":{"X":"` + secretValue + `"}}`}
}

func pricedConfig() *config.Config {
	cfg := testConfig()
	cfg.Models = map[string]config.Model{
		"sonnet": {Agent: "claude", PricedFor: "claude-sonnet-5-5", Input: 2, Cached: 0.1, Output: 10},
		"opus":   {Agent: "claude", PricedFor: "claude-opus-5-5", Input: 4, Cached: 0.2, Output: 20},
		"sol":    {Agent: "codex", Family: "sol", PricedFor: "gpt-6.1-sol", Input: 2, Cached: 0.1, Output: 10},
	}
	return cfg
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

func yesPtr() *bool { v := true; return &v }

func writeLocalConfig(t *testing.T, contents string) {
	t.Helper()
	path := filepath.Join(mustGetwd(t), ".duolog", "config.local.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakePace answers each agent's read with a fresh subscription snapshot whose
// week is 44% elapsed and used at the agent's share.
func fakePace(t *testing.T, week map[string]float64) {
	t.Helper()
	writeLocalConfig(t, "routing: {balance: pace}\n")
	old := paidruns.PaceRead
	t.Cleanup(func() { paidruns.PaceRead = old })
	paidruns.PaceRead = func(_ context.Context, p workerenv.Process) (quota.Snapshot, error) {
		used, ok := week[p.Agent]
		if !ok {
			return quota.Snapshot{}, context.DeadlineExceeded
		}
		now := time.Now()
		s := quota.Snapshot{ReadAt: now, Windows: []quota.Window{
			{Kind: "session", DurationMinutes: 300, UsedPercent: 10, ResetsAt: now.Add(time.Hour)},
			{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: used * 100, ResetsAt: now.Add(338688 * time.Second)},
		}}
		if p.Agent == "claude" {
			s.Billing = quota.BillingFacts{AuthMethod: "claude.ai", APIProvider: "firstParty", RouteKnown: true, WindowsAvailable: yesPtr()}
		} else {
			s.Billing = quota.BillingFacts{AccountType: "chatgpt", AuthMode: "chatgpt", RouteKnown: true, OrdinaryUsageAllowed: yesPtr()}
		}
		return s, nil
	}
}

func TestRoutePrintsPaceReason(t *testing.T) {
	isolatedRouteConfig(t)
	routeLead(t)
	fakePace(t, map[string]float64{"codex": .61, "claude": .30})
	out, err := executeBusCommand(t, "route", "implement")
	if err != nil {
		t.Fatal(out, err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "claude ") ||
		!strings.HasPrefix(lines[1], "reason: implement -> claude ") || !strings.HasSuffix(lines[1], ": codex 61% of the week at 44% elapsed") {
		t.Errorf("route output = %q", out)
	}
	out, err = executeBusCommand(t, "route", "implement", "--json")
	if err != nil {
		t.Fatal(out, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got["agent"] != "claude" || got["is_fallback"] != true || !strings.HasSuffix(got["reason"].(string), "codex 61% of the week at 44% elapsed") {
		t.Errorf("route json = %v", got)
	}
}

func TestRouteKeepsUseWithoutPaceReason(t *testing.T) {
	for name, week := range map[string]map[string]float64{
		"unknown quota":       {},
		"use on pace":         {"codex": .40, "claude": .30},
		"fallback also ahead": {"codex": .61, "claude": .61},
	} {
		isolatedRouteConfig(t)
		routeLead(t)
		fakePace(t, week)
		out, err := executeBusCommand(t, "route", "implement")
		if err != nil || !strings.HasPrefix(out, "codex ") || strings.Contains(out, "reason") {
			t.Errorf("%s: route output = %q, %v; want codex and no reason", name, out, err)
		}
	}
}

func TestRouteBalanceOffKeepsUse(t *testing.T) {
	isolatedRouteConfig(t)
	routeLead(t)
	fakePace(t, map[string]float64{"codex": .61, "claude": .30})
	writeLocalConfig(t, "routing: {balance: off}\n")
	out, err := executeBusCommand(t, "route", "implement")
	if err != nil || !strings.HasPrefix(out, "codex ") || strings.Contains(out, "reason") {
		t.Errorf("route output = %q, %v; want codex and no reason", out, err)
	}
}

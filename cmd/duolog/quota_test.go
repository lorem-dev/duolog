package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// freeClaudeRead is the reply key of a Claude quota read: the free worker's
// --settings prefix, then args.
func freeClaudeRead(args string) string {
	return "--settings " + workerenv.ClaudeSettings("", false, nil) + " " + args
}

// realQuotaGate gives this test's commands the real quota reader, through
// the fake agent binaries, and a temporary account state; TestMain's gate
// would answer every read itself. The returned func puts TestMain's gate
// back early, for a test that delegates through fake workers afterwards.
func realQuotaGate(t *testing.T) func() {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "accounts")
	old := delegate.NewGate
	delegate.NewGate = func(cfg *config.Config) (*paidruns.Gate, error) {
		st, err := paidruns.OpenUserState(dir)
		if err != nil {
			return nil, err
		}
		return &paidruns.Gate{Store: st, Cache: &paidruns.Cache{Read: quota.Reader{Version: "test"}.ReadProcess}, Config: cfg,
			Layers: paidruns.ReadLayers}, nil
	}
	restore := func() { delegate.NewGate = old }
	t.Cleanup(restore)
	return restore
}

func TestQuotaCommand(t *testing.T) {
	for _, mode := range []string{"text", "json", "missing", "both missing", "rejected", "logged out", "spend control"} {
		t.Run(mode, func(t *testing.T) {
			isolatedRouteConfig(t)
			realQuotaGate(t)
			t.Chdir(t.TempDir())
			reg, err := session.Open(".duolog")
			if err != nil {
				t.Fatal(err)
			}
			if err := reg.MarkUnavailable("claude", time.Time{}); err != nil {
				t.Fatal(err)
			}
			if err := reg.MarkUnavailable("codex", time.Time{}); err != nil {
				t.Fatal(err)
			}
			var dirs []string
			if mode != "both missing" {
				status := `{"loggedIn":true,"subscriptionType":"pro"}`
				if mode == "logged out" {
					status = `{"loggedIn":false}`
				}
				bin := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
					freeClaudeRead("-p"):                 {Stdout: `{"usage_report":{"rate_limits":{"limits":[{"kind":"session","percent":15,"resets_at":"2099-01-01T00:00:00Z"}],"extra_usage":{"is_enabled":false}}}}`},
					freeClaudeRead("auth status --json"): {Stdout: status},
				}})
				dirs = append(dirs, filepath.Dir(bin))
			}
			if mode != "missing" && mode != "both missing" {
				used := "13"
				if mode == "rejected" {
					used = "100"
				}
				flags := ""
				if mode == "spend control" {
					flags = `,"spendControlReached":true`
				}
				bin := testbin.Install(t, "codex", testbin.Scenario{RPCResponses: map[string]string{
					"initialize":              `{"id":1,"result":{}}`,
					"account/rateLimits/read": `{"id":2,"result":{"rateLimits":{"planType":"plus","primary":{"usedPercent":` + used + `,"windowDurationMins":300,"resetsAt":4070908800}` + flags + `}}}`,
					"account/read":            `{"id":3,"error":{"code":-32601}}`,
					"modelProvider/list":      `{"id":4,"error":{"code":-32601}}`,
				}})
				dirs = append(dirs, filepath.Dir(bin))
			}
			// Restrict PATH after building fakes, so no real agent is reachable.
			t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
			args := []string{"quota"}
			if mode == "json" {
				args = append(args, "--json")
			}
			out, err := executeBusCommand(t, args...)
			if mode == "both missing" {
				if err == nil || exitCode(err) != 1 {
					t.Fatal(out, err)
				}
			} else if err != nil {
				t.Fatal(out, err)
			}
			if mode == "json" {
				var result map[string]struct {
					Plan    string `json:"plan"`
					Cleared bool   `json:"cleared_unavailable"`
				}
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(out, err)
				}
				if len(result) != 2 || result["claude"].Plan != "pro" || !result["codex"].Cleared {
					t.Fatal(out)
				}
			} else {
				if !strings.Contains(out, "claude") || !strings.Contains(out, "codex") {
					t.Fatal(out)
				}
				if mode == "missing" && !strings.Contains(out, "codex   unavailable: not installed") {
					t.Fatal(out)
				}
				if mode == "text" && (!strings.Contains(out, "5h") || !strings.Contains(out, "15% used") || !strings.Contains(out, "extra usage off") || !strings.Contains(out, "cleared unavailable mark")) {
					t.Fatal(out)
				}
			}
			unavailable, err := reg.Unavailable()
			if err != nil {
				t.Fatal(err)
			}
			if unavailable["claude"] != (mode == "both missing" || mode == "logged out") {
				t.Fatal(out, unavailable)
			}
			if unavailable["codex"] != (mode == "missing" || mode == "both missing" || mode == "rejected" || mode == "spend control") {
				t.Fatal(out, unavailable)
			}
		})
	}
}

func TestQuotaDuplicateOutput(t *testing.T) {
	isolatedRouteConfig(t)
	realQuotaGate(t)
	data, err := os.ReadFile("../../internal/quota/testdata/codex-duplicate-limits.json")
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatal(err)
	}
	bin := testbin.Install(t, "codex", testbin.Scenario{RPCResponses: map[string]string{
		"initialize":              `{"id":1,"result":{}}`,
		"account/rateLimits/read": `{"id":2,"result":` + compact.String() + `}`,
		"account/read":            `{"id":3,"error":{"code":-32601}}`,
		"modelProvider/list":      `{"id":4,"error":{"code":-32601}}`,
	}})
	t.Setenv("PATH", filepath.Dir(bin))
	t.Chdir(t.TempDir())
	out, err := executeBusCommand(t, "quota")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "26% used") != 1 || strings.Count(out, "42% used") != 1 || !strings.Contains(out, "sol 5h") || strings.Contains(out, "credits available") {
		t.Fatal(out)
	}
	out, err = executeBusCommand(t, "quota", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]struct {
		ModelLimits map[string]json.RawMessage `json:"model_limits"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(out, err)
	}
	if limits := result["codex"].ModelLimits; len(limits) != 1 || limits["sol"] == nil {
		t.Fatal(out)
	}
}

func TestQuotaCreditsVisibility(t *testing.T) {
	for _, tc := range []struct {
		name           string
		has, unlimited bool
		balance        *string
		want           bool
	}{
		{"absent", false, false, nil, false},
		{"zero", false, false, quotaBalance("0"), false},
		{"decimal zero", false, false, quotaBalance("-0.00"), false},
		{"exponent zero", false, false, quotaBalance("0e2"), false},
		{"has credits", true, false, quotaBalance("0"), true},
		{"unlimited", false, true, nil, true},
		{"balance", false, false, quotaBalance("0.25"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var credits *quota.Credits
			if tc.name != "absent" {
				credits = &quota.Credits{HasCredits: tc.has, Unlimited: tc.unlimited, Balance: tc.balance}
			}
			var out bytes.Buffer
			err := printQuota(&out, map[string]quotaResult{
				"claude": {Snapshot: &quota.Snapshot{ExtraUsage: &quota.ExtraUsage{Enabled: false}}},
				"codex":  {Snapshot: &quota.Snapshot{Credits: credits}},
			}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "credits available") != tc.want || !strings.Contains(out.String(), "extra usage off") {
				t.Fatal(out.String())
			}
		})
	}
}

func quotaBalance(value string) *string { return &value }

func TestQuotaClaudeScopedWindow(t *testing.T) {
	isolatedRouteConfig(t)
	realQuotaGate(t)
	data, err := os.ReadFile("../../internal/quota/testdata/claude-scoped-usage.json")
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatal(err)
	}
	bin := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaudeRead("-p"):                 {Stdout: compact.String() + "\n"},
		freeClaudeRead("auth status --json"): {Stdout: `{"loggedIn":true,"subscriptionType":"max"}`},
	}})
	t.Setenv("PATH", filepath.Dir(bin))
	t.Chdir(t.TempDir())
	out, err := executeBusCommand(t, "quota")
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"weekly fable 0% used", "weekly  2% used", "session fable 3% used", "other   4% used"} {
		if !strings.Contains(out, label) {
			t.Fatalf("missing %q:\n%s", label, out)
		}
	}
	out, err = executeBusCommand(t, "quota", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]struct {
		Windows []struct {
			Model    string `json:"model"`
			Duration int    `json:"duration_minutes"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(out, err)
	}
	windows := result["claude"].Windows
	if len(windows) != 6 || windows[2].Model != "fable" || windows[4].Model != "fable" || windows[2].Duration != 10080 || windows[4].Duration != 300 {
		t.Fatal(out)
	}
	if windows[0].Model != "" || windows[3].Model != "" || windows[5].Model != "" {
		t.Fatal(out)
	}
}

func TestQuotaWithoutProjectStateCreatesNothing(t *testing.T) {
	isolatedRouteConfig(t)
	realQuotaGate(t)
	bin := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaudeRead("-p"):                 {Stdout: `{"usage_report":{"rate_limits":{"limits":[{"kind":"session","percent":15,"resets_at":"2099-01-01T00:00:00Z"}]}}}`},
		freeClaudeRead("auth status --json"): {Stdout: `{"loggedIn":true}`},
	}})
	t.Setenv("PATH", filepath.Dir(bin))
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := executeBusCommand(t, "quota"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".duolog")); !os.IsNotExist(err) {
		t.Fatal("quota created .duolog:", err)
	}
}

func TestQuotaWeeklyFamilyKindLabels(t *testing.T) {
	var out bytes.Buffer
	reset := time.Now().Add(time.Hour)
	err := printQuota(&out, map[string]quotaResult{
		"claude": {Snapshot: &quota.Snapshot{Windows: []quota.Window{
			{Kind: "weekly_opus", UsedPercent: 1, ResetsAt: reset},
			{Kind: "weekly_sonnet", UsedPercent: 2, ResetsAt: reset},
		}}},
		"codex": {Unavailable: "not installed"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"weekly opus 1% used", "weekly sonnet 2% used"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q:\n%s", want, out.String())
		}
	}
}

// subscriptionFakes installs Claude and Codex fakes that answer the free
// reads as first-party subscriptions, Codex with an API key route when
// apiKey is set, and keeps PATH to them.
func subscriptionFakes(t *testing.T, apiKey bool) {
	t.Helper()
	claude := testbin.Install(t, "claude", subscriptionReads("claude", testbin.Scenario{}))
	codexReads := subscriptionReads("codex", testbin.Scenario{})
	if apiKey {
		app := codexReads.Responses["app-server"]
		app.RPCResponses = maps.Clone(app.RPCResponses)
		app.RPCResponses["account/read"] = `{"jsonrpc":"2.0","id":3,"result":{"account":{"type":"apiKey"},"requiresOpenaiAuth":true}}`
		codexReads.Responses["app-server"] = app
	}
	codex := testbin.Install(t, "codex", codexReads)
	t.Setenv("PATH", filepath.Dir(claude)+string(os.PathListSeparator)+filepath.Dir(codex))
}

// quota reports every agent's worker, peer and lead, in text and JSON: the
// verdict and its signals, the account, the sessions of the agent and of
// all agents. A lead that was not observed is unknown, never the worker's
// verdict; an observed one is compared with the worker. No value shows.
func TestQuotaAndDoctorShapeReportsInQuota(t *testing.T) {
	isolatedRouteConfig(t)
	realQuotaGate(t)
	t.Chdir(t.TempDir())
	subscriptionFakes(t, true)
	const leadKey = "sk-ant-quota-lead"
	out, err := executeBusCommand(t, "quota")
	if err != nil {
		t.Fatal(out, err)
	}
	for _, want := range []string{"claude worker: subscription", "claude peer: subscription", "claude lead: unknown",
		"codex worker: paid", "duolog will not spend without paid_runs.codex", "sessions 0 of 10 in all"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// Review finding M1: the total is one line for both agents.
	if n := strings.Count(out, "in all"); n != 1 {
		t.Errorf("the total line appears %d times, want once:\n%s", n, out)
	}

	old := observeLead
	observeLead = func(agent string) (session.LeadRoute, bool) {
		if agent != "claude" {
			return session.LeadRoute{}, false
		}
		return session.LeadRoute{Agent: "claude", Argv: []string{"claude"}, Environ: []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=" + leadKey}}, true
	}
	t.Cleanup(func() { observeLead = old })
	out, err = executeBusCommand(t, "quota")
	if err != nil {
		t.Fatal(out, err)
	}
	if !strings.Contains(out, "claude lead: paid (env:ANTHROPIC_API_KEY); claude worker: subscription") {
		t.Errorf("no lead and worker comparison in\n%s", out)
	}
	out, err = executeBusCommand(t, "quota", "--json")
	if err != nil {
		t.Fatal(out, err)
	}
	var result map[string]struct {
		Plan    string                     `json:"plan"`
		Reports map[string]paidruns.Report `json:"reports"`
		Lead    string                     `json:"lead_unknown"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(out, err)
	}
	claude, codex := result["claude"], result["codex"]
	if claude.Reports["worker"].Assessment.Verdict != paidruns.Subscription || claude.Reports["peer"].Assessment.Shape != workerenv.Peer ||
		claude.Reports["lead"].Assessment.Verdict != paidruns.Paid || claude.Reports["worker"].TotalLimit != 10 {
		t.Fatalf("claude reports: %+v", claude.Reports)
	}
	if codex.Reports["worker"].Assessment.Verdict != paidruns.Paid || codex.Lead == "" || codex.Plan == "" {
		t.Fatalf("codex: %+v", codex)
	}
	if strings.Contains(out, leadKey) {
		t.Fatal("the JSON shows the lead's key")
	}
}

// A paid verdict alone does not fail quota; only no successful read does.
func TestLeadBillingIsObservationOnlyInQuota(t *testing.T) {
	isolatedRouteConfig(t)
	realQuotaGate(t)
	t.Chdir(t.TempDir())
	subscriptionFakes(t, true)
	if out, err := executeBusCommand(t, "quota"); err != nil {
		t.Fatalf("a paid verdict failed quota: %v\n%s", err, out)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := executeBusCommand(t, "quota"); err == nil || exitCode(err) != 1 {
		t.Fatalf("no read succeeded but quota passed: %v", err)
	}
}

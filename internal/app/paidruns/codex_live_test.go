package paidruns

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// codexLiveFixture holds the anonymised replies of a live Codex 0.165 read
// of a personal ChatGPT Plus account.
const codexLiveFixture = "../../quota/testdata/codex-0.165-personal-plus"

// codexLiveReply returns a fixture file as one JSON-RPC line that answers
// request id, the id quota's Codex read gives that method.
func codexLiveReply(t *testing.T, file string, id int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(codexLiveFixture, file))
	if err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	msg["jsonrpc"], msg["id"] = "2.0", id
	line, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

// A live Codex 0.165 read of a personal Plus account, served by a fake
// app-server that answers each method with its fixture file, reads as a
// subscription of a personal account with all of its windows usable.
func TestCodex0165PersonalPlusIsSubscription(t *testing.T) {
	identityHomes(t)
	codex := testbin.Install(t, "codex", testbin.Scenario{RPCResponses: map[string]string{
		"initialize":              `{"jsonrpc":"2.0","id":1,"result":{}}`,
		"account/rateLimits/read": codexLiveReply(t, "rate-limits-read.json", 2),
		"account/read":            codexLiveReply(t, "account-read.json", 3),
		"config/read":             codexLiveReply(t, "config-read.json", 4),
		"getAuthStatus":           codexLiveReply(t, "get-auth-status.json", 5),
		// Codex 0.165 has no provider list; duolog must not need one.
		"modelProvider/list":              codexLiveReply(t, "model-provider-list.json", 6),
		"modelProvider/capabilities/read": codexLiveReply(t, "model-provider-capabilities-read.json", 7),
	}})
	p, err := freeWorker("codex")()
	if err != nil {
		t.Fatal(err)
	}
	s, readErr := (quota.Reader{Version: "test", Timeout: 5 * time.Second}).ReadProcess(context.Background(), p)
	if readErr != nil {
		t.Fatal(readErr)
	}

	b := s.Billing
	if !b.RouteKnown || len(b.RouteSignals) != 0 || b.AccountType != "chatgpt" || b.AuthMethod != "chatgpt" || b.ProviderEnvKey != "" {
		t.Fatalf("route: %+v", b)
	}
	if b.OrdinaryUsageAllowed == nil || !*b.OrdinaryUsageAllowed || s.Rejected || s.SpendControlReached {
		t.Fatalf("permission: %+v", s)
	}
	if len(b.WorkspaceReasons) != 0 || b.OverageEnabled {
		t.Fatalf("workspace or overage evidence: %+v", b)
	}
	// rateLimitResetCredits is never read, and no request touches a reset.
	if b.ResetCredits != nil {
		t.Fatalf("reset credits read: %+v", b)
	}
	input, _ := os.ReadFile(codex + ".stdin")
	for _, line := range strings.Split(strings.TrimSpace(string(input)), "\n") {
		var msg struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatal(err)
		}
		if lower := strings.ToLower(msg.Method); strings.Contains(lower, "reset") || strings.Contains(lower, "consume") ||
			lower == "modelprovider/list" {
			t.Fatalf("read sent %s", msg.Method)
		}
	}
	if len(s.Windows) != 2 || s.Plan != "plus" {
		t.Fatalf("windows: %+v", s)
	}

	// Judge the read as of when it was taken: an hour before the session
	// window reset, which is the earliest reset in the fixture.
	at := s.Windows[0].ResetsAt.Add(-time.Hour)
	s.ReadAt = at
	a := paidrules.Evaluate(p, s, readErr, quota.RunEvidence{}, testConfig(), at)
	if a.Verdict != paidrules.Subscription || len(a.Signals) != 0 {
		t.Fatalf("verdict = %s %v, want subscription with no signal", a.Verdict, a.Signals)
	}
	if a.Account != "personal" || a.UsableShare != 1 || len(a.AccountReasons) != 0 {
		t.Fatalf("account = %s u=%v %v, want personal u=1", a.Account, a.UsableShare, a.AccountReasons)
	}
	if a.Shape != workerenv.Worker || a.Variant != workerenv.Free {
		t.Fatalf("assessed %s %s", a.Shape, a.Variant)
	}
}

// codexEnterpriseFixture holds the anonymised replies of a live Codex 0.166
// read of an enterprise seat on a per-person individual limit.
const codexEnterpriseFixture = "../../quota/testdata/codex-0.166-enterprise-individual"

// An enterprise seat with no primary or secondary window and
// ordinaryUsageAllowed null reads as a first-party subscription on its
// individual limit: a workspace account at the usable share. Its credits
// turn overage on, so the verdict is subscription_overage; without them it
// is subscription. Admission refuses a task as the limit nears full.
func TestCodex0166EnterpriseIndividualLimit(t *testing.T) {
	read := func(t *testing.T, change func(map[string]any), accounts ...func(map[string]any)) (workerenv.Process, quota.Snapshot) {
		t.Helper()
		identityHomes(t)
		reply := func(file string, id int) string {
			data, err := os.ReadFile(filepath.Join(codexEnterpriseFixture, file))
			if err != nil {
				t.Fatal(err)
			}
			var msg map[string]any
			if err := json.Unmarshal(data, &msg); err != nil {
				t.Fatal(err)
			}
			if file == "rate-limits-read.json" && change != nil {
				change(msg["result"].(map[string]any))
			}
			if file == "account-read.json" {
				for _, account := range accounts {
					account(msg["result"].(map[string]any)["account"].(map[string]any))
				}
			}
			msg["jsonrpc"], msg["id"] = "2.0", id
			line, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			return string(line)
		}
		testbin.Install(t, "codex", testbin.Scenario{RPCResponses: map[string]string{
			"initialize":              `{"jsonrpc":"2.0","id":1,"result":{}}`,
			"account/rateLimits/read": reply("rate-limits-read.json", 2),
			"account/read":            reply("account-read.json", 3),
			"config/read":             reply("config-read.json", 4),
			"getAuthStatus":           reply("get-auth-status.json", 5),
		}})
		p, err := freeWorker("codex")()
		if err != nil {
			t.Fatal(err)
		}
		s, err := (quota.Reader{Version: "test", Timeout: 5 * time.Second}).ReadProcess(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Windows) != 1 || s.Windows[0].Kind != "individual" {
			t.Fatalf("windows: %+v", s.Windows)
		}
		// Judge the read as of an hour before the limit resets.
		s.ReadAt = s.Windows[0].ResetsAt.Add(-time.Hour)
		return p, s
	}
	evaluate := func(p workerenv.Process, s quota.Snapshot) paidrules.Assessment {
		return paidrules.Evaluate(p, s, nil, quota.RunEvidence{}, testConfig(), s.ReadAt)
	}
	limits := func(reply map[string]any) []map[string]any {
		return []map[string]any{reply["rateLimits"].(map[string]any), reply["rateLimitsByLimitId"].(map[string]any)["codex"].(map[string]any)}
	}

	t.Run("live reply", func(t *testing.T) {
		p, s := read(t, nil)
		a := evaluate(p, s)
		if a.Verdict != paidrules.SubscriptionOverage || len(a.Signals) != 1 || a.Signals[0].Name != "overage:enabled" {
			t.Fatalf("verdict = %s %v, want subscription_overage with overage:enabled", a.Verdict, a.Signals)
		}
		if a.Account != "workspace" || a.UsableShare != 0.5 {
			t.Fatalf("account = %s u=%v %v", a.Account, a.UsableShare, a.AccountReasons)
		}
	})
	t.Run("no credits", func(t *testing.T) {
		p, s := read(t, func(r map[string]any) {
			for _, l := range limits(r) {
				l["credits"] = map[string]any{"hasCredits": false, "unlimited": false, "balance": "0"}
			}
		})
		if a := evaluate(p, s); a.Verdict != paidrules.Subscription || len(a.Signals) != 0 {
			t.Fatalf("verdict = %s %v, want subscription", a.Verdict, a.Signals)
		}
	})
	// The owner's decision: such a seat works by default, and one with an
	// unknown plan name and no workspace name is still a workspace.
	t.Run("unknown plan and no workspace name", func(t *testing.T) {
		p, s := read(t, nil, func(a map[string]any) { delete(a, "workspaceName") })
		a := evaluate(p, s)
		if a.Verdict == paidrules.Paid || a.Account != "workspace" || a.UsableShare != 0.5 {
			t.Fatalf("verdict = %s, account = %s u=%v", a.Verdict, a.Account, a.UsableShare)
		}
	})
	for name, plan := range map[string]string{"personal plan": "plus", "usage based plan": "enterprise_cbp_usage_based"} {
		t.Run(name, func(t *testing.T) {
			p, s := read(t, func(r map[string]any) {
				for _, l := range limits(r) {
					l["planType"] = plan
				}
			}, func(a map[string]any) { a["planType"] = plan })
			if a := evaluate(p, s); a.Verdict != paidrules.Paid || !slices.ContainsFunc(a.Signals, func(s workerenv.Signal) bool { return s.Name == "read:ordinary-usage" }) {
				t.Fatalf("verdict = %s %v, want paid", a.Verdict, a.Signals)
			}
		})
	}
	t.Run("explicit false", func(t *testing.T) {
		p, s := read(t, func(r map[string]any) { r["ordinaryUsageAllowed"] = false })
		if a := evaluate(p, s); a.Verdict != paidrules.Paid || a.Signals[0].Name != "read:ordinary-usage" {
			t.Fatalf("verdict = %s %v, want paid with read:ordinary-usage", a.Verdict, a.Signals)
		}
	})
	t.Run("spend control reached", func(t *testing.T) {
		p, s := read(t, func(r map[string]any) {
			for _, l := range limits(r) {
				l["spendControlReached"] = true
			}
		})
		if a := evaluate(p, s); a.Verdict != paidrules.Paid || !paidrules.FamilyBlocked(s, "") {
			t.Fatalf("verdict = %s %v, blocked %t", a.Verdict, a.Signals, paidrules.FamilyBlocked(s, ""))
		}
		if _, err := paidrules.WindowCosts(s, "sol", paidrules.Estimate{Input: 1000, Output: 100}, nil); err == nil {
			t.Fatal("window costs on a reached spend control")
		}
	})
	t.Run("limit used up", func(t *testing.T) {
		p, s := read(t, func(r map[string]any) {
			for _, l := range limits(r) {
				l["individualLimit"] = map[string]any{"limit": "400.00", "used": "400.00", "remainingPercent": 0, "resetsAt": 1794441600}
			}
		})
		if a := evaluate(p, s); a.Verdict != paidrules.Paid || a.Signals[0].Name != "read:ordinary-usage" {
			t.Fatalf("verdict = %s %v, want paid", a.Verdict, a.Signals)
		}
		costs, err := paidrules.WindowCosts(s, "sol", paidrules.Estimate{Input: 1000, Output: 100}, nil)
		if err != nil || paidrules.FitsWindows(costs, 1) {
			t.Fatalf("a full individual limit fits: %+v %v", costs, err)
		}
	})
}

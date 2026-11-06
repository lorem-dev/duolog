package paidruns

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

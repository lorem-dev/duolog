package codex_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/quota/quotatest"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// The app-server replies of internal/quota's tests: a chatgpt login on the
// built-in provider with a five-hour, a weekly and a per-model window.
var codexReplies = map[string]string{
	"initialize":              `{"id":1,"result":{}}`,
	"account/rateLimits/read": `{"jsonrpc":"2.0","id":2,"result":{"rateLimits":{"planType":"plus","primary":{"usedPercent":13,"windowDurationMins":300,"resetsAt":4070908800},"secondary":{"usedPercent":20,"windowDurationMins":10080,"resetsAt":4071513600},"credits":{"hasCredits":true,"unlimited":false,"balance":"10"},"ordinaryUsageAllowed":true,"accountId":"PRIVATE"},"rateLimitsByLimitId":{"sonnet":{"primary":{"usedPercent":30,"windowDurationMins":60,"resetsAt":4070908800}}}}}`,
	"account/read":            `{"jsonrpc":"2.0","id":3,"result":{"account":{"type":"chatgpt","email":"PRIVATE","planType":"plus"},"requiresOpenaiAuth":true}}`,
	"config/read":             `{"jsonrpc":"2.0","id":4,"result":{"config":{"model_provider":null,"model_providers":{},"profile":null}}}`,
	"getAuthStatus":           `{"jsonrpc":"2.0","id":5,"result":{"authMethod":"chatgpt","authToken":null,"requiresOpenaiAuth":true}}`,
}

// TestReaderContract runs the Reader contract on Codex's Quota and the fake
// app-server with the replies of internal/quota's tests. The snapshot it
// must find is the one internal/quota reads from the same replies.
func TestReaderContract(t *testing.T) {
	quotatest.ReaderContract(t, func(t *testing.T) quotatest.Case {
		bin := testbin.Install(t, "codex", testbin.Scenario{SleepMS: 10000, RPCResponses: codexReplies})
		p := launch.Process{Agent: "codex", Prefix: []string{"codex"}, Env: []string{"PATH=" + filepath.Dir(bin)}}
		r := codex.Quota{Version: "test", Timeout: 5 * time.Second}
		// Written out from codexReplies, not read back through quota.Reader.
		balance, allowed := "10", true
		want := account.Snapshot{
			Plan: "plus",
			Windows: []account.Window{
				{Kind: "session", DurationMinutes: 300, UsedPercent: 13, ResetsAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)},
				{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 20, ResetsAt: time.Date(2099, 1, 8, 0, 0, 0, 0, time.UTC)},
			},
			ModelLimits: map[string][]account.Window{"sonnet": {
				{Kind: "60m", DurationMinutes: 60, UsedPercent: 30, ResetsAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)},
			}},
			Credits: &account.Credits{HasCredits: true, Balance: &balance},
			Billing: account.BillingFacts{AuthMethod: "chatgpt", AccountType: "chatgpt", RouteKnown: true, OrdinaryUsageAllowed: &allowed, OverageEnabled: true},
		}
		failing := p
		failing.Env = []string{"PATH=" + t.TempDir()}
		other := p
		other.Agent = "claude"
		return quotatest.Case{Reader: r, Read: p, Failing: failing, Other: other, Want: want}
	})
}

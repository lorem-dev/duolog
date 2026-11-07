package codex_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/quota/quotatest"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/quota"
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
		want, err := quota.Reader{Version: r.Version, Timeout: r.Timeout}.ReadProcess(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		failing := p
		failing.Env = []string{"PATH=" + t.TempDir()}
		other := p
		other.Agent = "claude"
		return quotatest.Case{Reader: r, Read: p, Failing: failing, Other: other, Want: want}
	})
}

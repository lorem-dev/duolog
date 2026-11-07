package claude_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/app/quota/quotatest"
	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// claudeUsage is the /usage reply of internal/quota's tests.
const claudeUsage = `{"usage_report":{"rate_limits":{"limits":[{"kind":"session","percent":15,"resets_at":"2099-01-01T00:00:00Z","severity":"normal","is_active":false},{"kind":"weekly_all","percent":50,"resets_at":"2099-01-08T00:00:00Z"}],"extra_usage":{"is_enabled":false,"used_credits":0,"monthly_limit":null}}}}`

// TestReaderContract runs the Reader contract on Claude's Quota and the fake
// agent binary with the replies of internal/quota's tests. The snapshot it
// must find is the one internal/quota reads from the same replies.
func TestReaderContract(t *testing.T) {
	quotatest.ReaderContract(t, func(t *testing.T) quotatest.Case {
		bin := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
			"-p":                 {Stdout: "{\"type\":\"system\"}\n" + claudeUsage + "\n"},
			"auth status --json": {Stdout: `{"loggedIn":true,"subscriptionType":"pro","authMethod":"claude.ai","email":"PRIVATE"}`},
		}})
		p := launch.Process{Agent: "claude", Prefix: []string{"claude"}, Env: []string{"PATH=" + filepath.Dir(bin)}}
		r := claude.Quota{Version: "test", Timeout: 5 * time.Second}
		// Written out from claudeUsage and the auth status reply above, not
		// read back through quota.Reader: the weekly window has no severity in
		// the reply, so it reads as unknown.
		want := account.Snapshot{
			Plan: "pro",
			Windows: []account.Window{
				{Kind: "session", DurationMinutes: 300, UsedPercent: 15, ResetsAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Severity: "normal"},
				{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 50, ResetsAt: time.Date(2099, 1, 8, 0, 0, 0, 0, time.UTC), Severity: "unknown"},
			},
			ExtraUsage: &account.ExtraUsage{},
			Billing:    account.BillingFacts{AuthMethod: "claude.ai", RouteKnown: true},
		}
		failing := p
		failing.Env = []string{"PATH=" + t.TempDir()}
		other := p
		other.Agent = "codex"
		return quotatest.Case{Reader: r, Read: p, Failing: failing, Other: other, Want: want}
	})
}

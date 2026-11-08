package quota

import (
	"context"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const claudeSignedIn = `{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max"}`

// TestClaudeReadWithoutAReportIsRetried: a /usage that fails or stays silent
// and prints no usage report said nothing about the account, so it is read
// once more, and the second answer is the read.
func TestClaudeReadWithoutAReportIsRetried(t *testing.T) {
	for name, first := range map[string]testbin.Scenario{
		"failed exit":  {Exit: 1},
		"empty reply":  {},
		"other output": {Stdout: "{\"type\":\"system\"}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
				freeClaude("-p"):                 {Sequence: []testbin.Scenario{first, {Stdout: claudeReply + "\n"}}},
				freeClaude("auth status --json"): {Stdout: claudeSignedIn},
			}})
			s, err := (Reader{Timeout: 10 * time.Second}).Read(context.Background(), "claude")
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Windows) != 2 || !s.Billing.RouteKnown {
				t.Fatalf("%+v", s)
			}
			if n := countCalls(t, claude, "/usage"); n != 2 {
				t.Fatalf("%d /usage calls; want 2", n)
			}
		})
	}
}

// TestClaudeSilentReadFailsAfterOneRetry: a second read that says nothing is
// a failed read, and still one that a later read may clear.
func TestClaudeSilentReadFailsAfterOneRetry(t *testing.T) {
	isolate(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p"):                 {Exit: 1},
		freeClaude("auth status --json"): {Stdout: claudeSignedIn},
	}})
	_, err := (Reader{Timeout: 10 * time.Second}).Read(context.Background(), "claude")
	if !account.IsTransient(err) {
		t.Fatalf("not transient: %v", err)
	}
	if n := countCalls(t, claude, "/usage"); n != 2 {
		t.Fatalf("%d /usage calls; want 2", n)
	}
}

// TestClaudeReadWithEvidenceIsNotTransient: a failed read that shows anything
// about the account or its route has an answer. It is not read again, and no
// earlier read may stand in for it.
func TestClaudeReadWithEvidenceIsNotTransient(t *testing.T) {
	const refused = `{"usage_report":{"rate_limits_available":false}}` + "\n"
	const invalid = `{"usage_report":{"rate_limits":{"limits":[{"kind":"session","percent":400,"resets_at":"2099-01-01T00:00:00Z"}]}}}` + "\n"
	for _, tc := range []struct {
		name        string
		usage, auth testbin.Scenario
	}{
		{"report then a failed exit", testbin.Scenario{Stdout: refused, Exit: 1}, testbin.Scenario{Stdout: claudeSignedIn}},
		{"report without windows", testbin.Scenario{Stdout: refused}, testbin.Scenario{Stdout: claudeSignedIn}},
		{"invalid window", testbin.Scenario{Stdout: invalid}, testbin.Scenario{Stdout: claudeSignedIn}},
		{"logged out", testbin.Scenario{}, testbin.Scenario{Stdout: `{"loggedIn":false}`}},
		{"api key login", testbin.Scenario{}, testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"api_key"}`}},
		{"third party provider", testbin.Scenario{}, testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"bedrock"}`}},
		{"unread login", testbin.Scenario{}, testbin.Scenario{Exit: 1}},
		{"failed exit, logged out", testbin.Scenario{Exit: 1}, testbin.Scenario{Stdout: `{"loggedIn":false}`}},
		{"failed exit, api key login", testbin.Scenario{Exit: 1}, testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"api_key"}`}},
		{"failed exit, third party provider", testbin.Scenario{Exit: 1}, testbin.Scenario{Stdout: `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"bedrock"}`}},
		{"failed exit, unread login", testbin.Scenario{Exit: 1}, testbin.Scenario{Exit: 1}},
		{"failed exit, unparsable login", testbin.Scenario{Exit: 1}, testbin.Scenario{Stdout: "not json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
				freeClaude("-p"):                 tc.usage,
				freeClaude("auth status --json"): tc.auth,
			}})
			_, err := (Reader{Timeout: 10 * time.Second}).Read(context.Background(), "claude")
			if err == nil || account.IsTransient(err) {
				t.Fatalf("want a definite failure, got %v", err)
			}
			if n := countCalls(t, claude, "/usage"); n != 1 {
				t.Fatalf("%d /usage calls; want 1", n)
			}
		})
	}
}

// TestReadOnceDoesNotRetryASilentRead: Once gives up after one start here too.
func TestReadOnceDoesNotRetryASilentRead(t *testing.T) {
	isolate(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p"):                 {Exit: 1},
		freeClaude("auth status --json"): {Stdout: claudeSignedIn},
	}})
	_, err := (Reader{Timeout: 10 * time.Second, Once: true}).Read(context.Background(), "claude")
	if !account.IsTransient(err) {
		t.Fatalf("not transient: %v", err)
	}
	if n := countCalls(t, claude, "/usage"); n != 1 {
		t.Fatalf("%d /usage calls; want 1", n)
	}
}

// TestTimedOutReadIsTransient: a read that ran out of time found nothing.
func TestTimedOutReadIsTransient(t *testing.T) {
	if !account.IsTransient(ErrTimedOut) {
		t.Fatal("a timed-out read is not transient")
	}
}

package quota

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// countCalls counts the fake's calls whose arguments hold word.
func countCalls(t *testing.T, bin, word string) int {
	t.Helper()
	n := 0
	for _, call := range recordedCalls(t, bin) {
		if slices.Contains(call, word) {
			n++
		}
	}
	return n
}

// TestClaudeSlowStartsOutlastOneBudget reproduces a loaded machine: /usage
// and auth status each start well inside their own bounds, but they run one
// after the other inside the read's single budget, so together they pass it
// and the read times out. The retry with a longer budget lets the same two
// starts finish.
func TestClaudeSlowStartsOutlastOneBudget(t *testing.T) {
	isolate(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p"):                 {Stdout: claudeReply + "\n", SleepMS: 1200},
		freeClaude("auth status --json"): {Stdout: `{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max"}`, SleepMS: 1200},
	}})
	s, err := (Reader{Timeout: 2 * time.Second}).Read(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Windows) != 2 || s.Plan != "max" || len(s.Billing.RouteSignals) != 0 {
		t.Fatalf("%+v", s)
	}
	// The first read timed out in auth status, after /usage had answered.
	if usage, auth := countCalls(t, claude, "/usage"), countCalls(t, claude, "auth"); usage != 2 || auth != 2 {
		t.Fatalf("/usage %d, auth status %d calls; want 2 and 2", usage, auth)
	}
}

// TestReadRetriesATimeoutOnce: a read that times out again fails, after
// exactly one retry, with the timeout error.
func TestReadRetriesATimeoutOnce(t *testing.T) {
	isolate(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{SleepMS: 60000})
	start := time.Now()
	_, err := (Reader{Timeout: 300 * time.Millisecond}).Read(context.Background(), "claude")
	if !errors.Is(err, ErrTimedOut) {
		t.Fatal(err)
	}
	if time.Since(start) > 15*time.Second {
		t.Fatal("unbounded read")
	}
	if n := countCalls(t, claude, "/usage"); n != 2 {
		t.Fatalf("%d /usage calls; want 2", n)
	}
}

// TestReadNoRetryAfterCallerCancels: the caller's own deadline ends the read
// without a retry; only the read's own budget is retried.
func TestReadNoRetryAfterCallerCancels(t *testing.T) {
	isolate(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{SleepMS: 60000})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := (Reader{Timeout: 10 * time.Second}).Read(ctx, "claude")
	if !errors.Is(err, ErrTimedOut) {
		t.Fatal(err)
	}
	if n := countCalls(t, claude, "/usage"); n != 1 {
		t.Fatalf("%d /usage calls; want 1", n)
	}
}

// TestCodexTimeoutIsRetried: a Codex read that runs past its budget is read
// once more too; an app-server that answers the second time gives the read.
func TestCodexTimeoutIsRetried(t *testing.T) {
	isolate(t)
	codex := testbin.Install(t, "codex", testbin.Scenario{Sequence: []testbin.Scenario{
		{RPCResponses: map[string]string{"initialize": `{"id":1,"result":{}}`}},
		{RPCResponses: codexRoute(map[string]string{
			"initialize": `{"id":1,"result":{}}`, "account/rateLimits/read": codexReply,
			"account/read": codexAccountReply,
		})},
	}})
	s, err := (Reader{Version: "test", Timeout: 2 * time.Second}).Read(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Windows) != 2 || s.Plan != "plus" {
		t.Fatalf("%+v", s)
	}
	if n := len(recordedCalls(t, codex)); n != 2 {
		t.Fatalf("%d codex starts; want 2", n)
	}
}

// TestReadOnceDoesNotRetry: a Reader with Once set fails a timed-out read
// after one start.
func TestReadOnceDoesNotRetry(t *testing.T) {
	isolate(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{SleepMS: 60000})
	_, err := (Reader{Timeout: time.Second, Once: true}).Read(context.Background(), "claude")
	if !errors.Is(err, ErrTimedOut) {
		t.Fatal(err)
	}
	if n := countCalls(t, claude, "/usage"); n != 1 {
		t.Fatalf("%d /usage calls; want 1", n)
	}
}

// TestClaudeAuthTimeoutIsRetried: an auth status that runs past its own bound
// is a timed-out read too; the retry reads the route instead of leaving it
// unknown.
func TestClaudeAuthTimeoutIsRetried(t *testing.T) {
	defer func(d time.Duration) { claudeAuthTimeout = d }(claudeAuthTimeout)
	claudeAuthTimeout = 500 * time.Millisecond
	isolate(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p"): {Stdout: claudeReply + "\n"},
		freeClaude("auth status --json"): {Sequence: []testbin.Scenario{
			{SleepMS: 10000},
			{Stdout: `{"loggedIn":true,"authMethod":"claude.ai"}`},
		}},
	}})
	s, err := (Reader{Timeout: 5 * time.Second}).Read(context.Background(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Billing.RouteSignals) != 0 || !s.Billing.RouteKnown {
		t.Fatalf("%+v", s.Billing)
	}
	if n := countCalls(t, claude, "auth"); n != 2 {
		t.Fatalf("%d auth status calls; want 2", n)
	}
}

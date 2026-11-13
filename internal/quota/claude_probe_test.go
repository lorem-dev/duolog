package quota

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Kills discarding explicit rejected windows when the probe exits nonzero.
func TestClaudeProbeKeepsWindowsOnFailure(t *testing.T) {
	isolate(t)
	event := `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1794744000,"rateLimitType":"seven_day","overageStatus":"allowed","overageResetsAt":1796083200,"isUsingOverage":false,"unifiedWindows":{"seven_day":{"utilization":1,"resetsAt":1794744000,"status":"rejected"}}}}`
	bin := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p /usage"):          {Stdout: `{"type":"result","result":"What's contributing to your usage?"}` + "\n"},
		freeClaude("auth status --json"): {Stdout: claudeSignedIn},
		freeClaude("-p ok"):              {Stdout: event + "\n", Exit: 1},
	}})
	s, err := (Reader{Once: true, Timeout: 30 * time.Second}).Read(context.Background(), "claude")
	if err != nil || !s.Rejected || len(s.Windows) != 1 || s.Windows[0].Kind != "weekly_all" || s.Windows[0].UsedPercent != 100 || !s.Windows[0].Rejected() || s.Source != "rate_limit_event (haiku probe)" {
		t.Fatalf("snapshot %+v, err %v", s, err)
	}
	if countCalls(t, bin, "ok") != 1 {
		t.Fatalf("calls %v", recordedCalls(t, bin))
	}
}

// Kills returning no-quota without probing a confirmed subscription, or probing paid auth routes.
func TestClaudeWeeklyProbe(t *testing.T) {
	for _, method := range []string{"claude.ai", "api_key", "third_party", "bedrock"} {
		t.Run(method, func(t *testing.T) {
			isolate(t)
			methodName, provider := method, "firstParty"
			if method == "bedrock" {
				methodName, provider = "claude.ai", "bedrock"
			}
			event := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1794744000,"rateLimitType":"seven_day","overageStatus":"allowed","overageResetsAt":1796083200,"isUsingOverage":false,"unifiedWindows":{"seven_day":{"utilization":0.09,"resetsAt":1794744000,"status":"allowed"}}}}`
			bin := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
				freeClaude("-p /usage"):          {Stdout: `{"type":"result","result":"What's contributing to your usage?"}` + "\n"},
				freeClaude("auth status --json"): {Stdout: `{"loggedIn":true,"authMethod":"` + methodName + `","apiProvider":"` + provider + `","subscriptionType":"max"}`},
				freeClaude("-p ok"):              {Stdout: event + "\n"},
			}})
			// The bound only stops a hung read; a windows-latest runner took
			// more than 3 s to start the fake claude.exe here.
			s, err := (Reader{Once: true, Timeout: 30 * time.Second}).Read(context.Background(), "claude")
			calls := recordedCalls(t, bin)
			if method != "claude.ai" {
				if err == nil || len(calls) != 2 {
					t.Fatalf("paid route: %v, calls %v", err, calls)
				}
				return
			}
			if err != nil || len(s.Windows) != 1 || s.Windows[0].UsedPercent != 9 || s.Source != "rate_limit_event (haiku probe)" || !s.Billing.OverageEnabled {
				t.Fatalf("snapshot %+v, err %v", s, err)
			}
			if len(calls) != 3 || !strings.Contains(strings.Join(calls[2], " "), "--model haiku --output-format stream-json --verbose --no-session-persistence") {
				t.Fatalf("calls %v", calls)
			}
		})
	}
}

// Kills probing twice when a silent /usage read is retried after an empty probe.
func TestClaudeProbeRunsOnceAcrossRetry(t *testing.T) {
	isolate(t)
	bin := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p /usage"):          {},
		freeClaude("auth status --json"): {Stdout: claudeSignedIn},
		freeClaude("-p ok"):              {},
	}})
	_, err := (Reader{Timeout: 30 * time.Second}).Read(context.Background(), "claude")
	if err == nil || countCalls(t, bin, "/usage") != 2 || countCalls(t, bin, "ok") != 1 {
		t.Fatalf("err %v calls %v", err, recordedCalls(t, bin))
	}
}

// Kills losing paid-overage evidence when a probe prints no usable windows.
func TestClaudeProbeFailureKeepsOverage(t *testing.T) {
	isolate(t)
	testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		freeClaude("-p /usage"):          {},
		freeClaude("auth status --json"): {Stdout: claudeSignedIn},
		freeClaude("-p ok"):              {Stdout: `{"type":"rate_limit_event","rate_limit_info":{"isUsingOverage":true}}` + "\n"},
	}})
	s, err := (Reader{Once: true, Timeout: 30 * time.Second}).Read(context.Background(), "claude")
	if err == nil || !s.Billing.OverageEnabled || account.IsTransient(err) {
		t.Fatalf("snapshot %+v err %v", s, err)
	}
}

// Kills probing a process that the gate already knows has a paid route, even if auth names a subscription.
func TestClaudeProbeSkipsPaidProcess(t *testing.T) {
	isolate(t)
	bin := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"-p /usage":          {},
		"auth status --json": {Stdout: claudeSignedIn},
		"-p ok":              {},
	}})
	p := workerenv.Process{Agent: "claude", Shape: workerenv.Worker, Variant: workerenv.Free, Prefix: []string{bin}, Env: []string{}, Signals: []workerenv.Signal{{Name: "apiKeyHelper", Layer: "user"}}}
	_, err := (Reader{Once: true, Timeout: 30 * time.Second}).ReadProcess(context.Background(), p)
	if err == nil || len(recordedCalls(t, bin)) != 2 {
		t.Fatalf("err %v calls %v", err, recordedCalls(t, bin))
	}
}

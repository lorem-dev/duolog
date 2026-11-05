package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// billingStream is the synthetic stream-json capture of a worker that went
// into paid overage before its final result.
func billingStream(t *testing.T) []string {
	t.Helper()
	return strings.SplitAfter(strings.TrimSuffix(string(read(t, filepath.Join("testdata", "claude-billing-stream.ndjson"))), "\n"), "\n")
}

// A worker's stream carries noise, tool output and model text around the
// rate limit events. The final result gives the answer, session, tokens and
// cost; the earlier overage and workspace categories survive it, and text
// that only talks about overage changes nothing.
func TestClaudeStreamFinalResultKeepsEarlierOverage(t *testing.T) {
	s := spec(t, "claude")
	fake := install(t, &s, testbin.Scenario{Stdout: strings.Join(billingStream(t), "")})
	r, err := Run(context.Background(), s)
	if err != nil || !r.Done || r.Summary != "Implemented." || r.SessionID != "stream-session" {
		t.Fatalf("%+v %v", r, err)
	}
	// The final aggregate replaces the per-message usage; it is not added to it.
	if r.Usage != (Usage{Input: 2220, Cached: 2000, Output: 30}) {
		t.Fatalf("usage %+v, want the final result's 2220 input, 2000 cached, 30 output", r.Usage)
	}
	if r.ReportedUSD == nil || *r.ReportedUSD != 0.4321 {
		t.Fatalf("reported cost %v, want 0.4321", r.ReportedUSD)
	}
	want := quota.RunEvidence{Expected: true, Seen: true, UsingOverage: true, OverageEnabled: true, WorkspaceReasons: []string{"seat_tier"}}
	if !reflect.DeepEqual(r.Billing, want) {
		t.Fatalf("billing %+v, want %+v", r.Billing, want)
	}
	if string(read(t, filepath.Join(s.TaskDir, "answer.json"))) != goodAnswer {
		t.Fatal("answer not saved")
	}
	containsArgs(t, readArgs(t, fake), "--output-format", "stream-json", "--verbose", "--json-schema")
}

// Billing that the stream measured is kept on every exit: a cancelled run,
// a failed exit, a turn cap and a result without an answer. A run without a
// rate limit event was expected to send one and did not.
func TestClaudeStreamPartialCancelAndTurnCap(t *testing.T) {
	lines := billingStream(t)
	prefix := strings.Join(lines[:8], "")
	if !strings.Contains(lines[7], `"rateLimitType":"overage"`) {
		t.Fatal("fixture changed: line 8 is not the overage event")
	}
	overage := quota.RunEvidence{Expected: true, Seen: true, UsingOverage: true, OverageEnabled: true}
	partial := Usage{Input: 1110, Cached: 1000, Output: 8}

	t.Run("cancelled", func(t *testing.T) {
		s := spec(t, "claude")
		s.Timeout = time.Minute
		install(t, &s, testbin.Scenario{Stdout: prefix, SleepMS: 60000})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			capture := filepath.Join(s.TaskDir, "claude.stdout.json")
			for ctx.Err() == nil {
				if data, err := os.ReadFile(capture); err == nil && strings.Contains(string(data), `"rateLimitType":"overage"`) {
					cancel()
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
		r, err := Run(ctx, s)
		if !errors.Is(err, context.Canceled) || !r.Interrupted || r.Done {
			t.Fatalf("%+v %v", r, err)
		}
		if r.Usage != partial || !reflect.DeepEqual(r.Billing, overage) || r.ReportedUSD != nil {
			t.Fatalf("usage %+v, billing %+v, cost %v; want %+v, %+v, no cost", r.Usage, r.Billing, r.ReportedUSD, partial, overage)
		}
	})

	for _, tc := range []struct {
		name, result string
		exit, turns  int
		cost         float64
		turnCap      bool
	}{
		{name: "failed exit", exit: 1, cost: 0.25, result: `{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"stream-session","total_cost_usd":0.25,"usage":{"input_tokens":3,"cache_read_input_tokens":7,"output_tokens":2}}`},
		{name: "turn cap", exit: 1, turns: 2, cost: 0.5, turnCap: true, result: `{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":3,"session_id":"stream-session","total_cost_usd":0.5,"usage":{"input_tokens":3,"cache_read_input_tokens":7,"output_tokens":2}}`},
		{name: "missing answer", cost: 0, result: `{"type":"result","subtype":"success","is_error":false,"session_id":"stream-session","total_cost_usd":0,"usage":{"input_tokens":3,"cache_read_input_tokens":7,"output_tokens":2}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := spec(t, "claude")
			s.MaxTurns = tc.turns
			install(t, &s, testbin.Scenario{Exit: tc.exit, Stdout: prefix + tc.result + "\n"})
			r, err := Run(context.Background(), s)
			if err == nil || r.Done || errors.Is(err, ErrTurnCap) != tc.turnCap || r.TurnsEnded != tc.turnCap {
				t.Fatalf("%+v %v", r, err)
			}
			// A reported zero is a cost; it is not the same as no cost.
			if r.ReportedUSD == nil || *r.ReportedUSD != tc.cost {
				t.Fatalf("reported cost %v, want %v", r.ReportedUSD, tc.cost)
			}
			if r.Usage != (Usage{Input: 10, Cached: 7, Output: 2}) || !reflect.DeepEqual(r.Billing, overage) {
				t.Fatalf("usage %+v, billing %+v", r.Usage, r.Billing)
			}
		})
	}

	t.Run("no rate limit event", func(t *testing.T) {
		s := spec(t, "claude")
		install(t, &s, testbin.Scenario{Stdout: lines[0] + `{"type":"result","subtype":"success","is_error":false,"session_id":"stream-session","usage":{"input_tokens":3},"structured_output":` + goodAnswer + "}\n"})
		r, err := Run(context.Background(), s)
		if err != nil || !r.Done {
			t.Fatalf("%+v %v", r, err)
		}
		if !reflect.DeepEqual(r.Billing, quota.RunEvidence{Expected: true}) || !r.Billing.Charged() {
			t.Fatalf("billing %+v, want expected but not seen", r.Billing)
		}
		if r.ReportedUSD != nil {
			t.Fatalf("reported cost %v, want none", *r.ReportedUSD)
		}
	})

	t.Run("invalid cost", func(t *testing.T) {
		for _, cost := range []string{`-0.5`, `1e999`, `-1e999`, `"7f3a"`, `"0.75"`, `true`, `{"usd":"7f3a"}`, `[0.1]`, `null`} {
			path := filepath.Join(t.TempDir(), "claude.stdout.json")
			stream := `{"type":"result","subtype":"success","session_id":"stream-session","total_cost_usd":` + cost + `,"usage":{"input_tokens":3},"structured_output":` + goodAnswer + "}\n"
			if err := os.WriteFile(path, []byte(stream), 0600); err != nil {
				t.Fatal(err)
			}
			var r Result
			answer, err := readClaudeStream(path, &r)
			if err != nil || string(answer) != goodAnswer || r.SessionID != "stream-session" || r.Usage.Input != 3 {
				t.Fatalf("cost %s: %s %+v %v", cost, answer, r, err)
			}
			if r.ReportedUSD != nil {
				t.Fatalf("cost %s: reported %v, want none", cost, *r.ReportedUSD)
			}
			if text := fmt.Sprintf("%+v %v", r, err); strings.Contains(text, "7f3a") || strings.Contains(text, "1e999") {
				t.Fatalf("cost %s leaked: %s", cost, text)
			}
		}
		for cost, want := range map[string]float64{`0`: 0, `1.25`: 1.25, `0.000125`: 0.000125} {
			path := filepath.Join(t.TempDir(), "claude.stdout.json")
			if err := os.WriteFile(path, []byte(`{"type":"result","total_cost_usd":`+cost+`,"structured_output":`+goodAnswer+"}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var r Result
			if _, err := readClaudeStream(path, &r); err != nil || r.ReportedUSD == nil || *r.ReportedUSD != want {
				t.Fatalf("cost %s: reported %v %v, want %v", cost, r.ReportedUSD, err, want)
			}
		}
	})
}

func readArgs(t *testing.T, fake string) []string {
	t.Helper()
	var args []string
	if err := json.Unmarshal(read(t, fake+".args.json"), &args); err != nil {
		t.Fatal(err)
	}
	return args
}

package delegate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/auth"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// An ask the lead or the person settled is logged with rule "ask lead" and
// the answer's kind; it counts as an ask, and a denied one as a denial too.
func TestTelemetryAskLeadCountsAsAsk(t *testing.T) {
	dir := t.TempDir()
	var lines []byte
	for _, r := range []gate.Record{
		{Action: gate.Action{Kind: "read", Role: "implement"}, Decision: gate.Decision{Kind: gate.Allow, Rule: "ask lead"}},
		{Action: gate.Action{Kind: "exec", Role: "implement"}, Decision: gate.Decision{Kind: gate.Deny, Rule: "ask lead"}},
		{Action: gate.Action{Kind: "exec", Role: "implement"}, Decision: gate.Decision{Kind: gate.Deny, Rule: "worker depth"}},
		{Action: gate.Action{Kind: "read", Role: "implement"}, Decision: gate.Decision{Kind: gate.Allow, Rule: "role sandbox"}},
		{Action: gate.Action{Kind: "read", Role: "review"}, Decision: gate.Decision{Kind: gate.Allow, Rule: "ask lead"}},
	} {
		line, _ := json.Marshal(r)
		lines = append(append(lines, line...), '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "decisions.ndjson"), lines, 0600); err != nil {
		t.Fatal(err)
	}
	denials, asks, err := (&learnRun{stateDir: dir, role: "implement"}).gateCounts()
	if err != nil || asks != 2 || denials != 2 {
		t.Fatalf("asks=%d denials=%d %v", asks, denials, err)
	}
}

// A delegation draws its own run id and gives it to every worker it starts
// as DUOLOG_RUN, so the worker's gate events carry it; its attempts carry
// the same id, never the vendor's session id. The next delegation draws a
// new one.
func TestTelemetryRunIDPassedToWorker(t *testing.T) {
	h := newHarness(t)
	h.limited() // the first agent's quota ends; the run falls back
	seen, opts := h.observed()
	if _, err := h.runWith("implement", opts); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(2)
	env := func(i int) string {
		for _, pair := range h.calls[i].Env {
			if v, ok := strings.CutPrefix(pair, "DUOLOG_RUN="); ok {
				return v
			}
		}
		return ""
	}
	run := env(0)
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(run) || env(1) != run {
		t.Fatalf("worker run ids %q %q", run, env(1))
	}
	if len(*seen) != 2 || (*seen)[0].Run != run || (*seen)[1].Run != run {
		t.Fatalf("attempts: %+v", *seen)
	}
	for _, a := range *seen {
		if a.Run == "worker-id" {
			t.Fatal("a vendor id became the run id")
		}
	}
	if _, err := h.runWith("implement", opts); err != nil {
		t.Fatal(err)
	}
	if next := env(2); next == run || len(*seen) != 3 || (*seen)[2].Run != next {
		t.Fatalf("a new delegation reused the run id: %q", next)
	}
}

// observed collects the attempts a delegation reports.
func (h *harness) observed() (*[]Attempt, Options) {
	var seen []Attempt
	return &seen, Options{Observe: func(a Attempt) { seen = append(seen, a) }}
}

// A failed attempt and the success after it are both reported, with learning
// off as with it on: quota and a missing binary fall back to the other agent,
// a refused login runs again after a login that works, and a parse failure
// ends the delegation, so the success is the next delegation's first attempt.
func TestAttemptObserverLearningOff(t *testing.T) {
	type step struct{ outcome, code string }
	cases := []struct {
		name    string
		prepare func(h *harness)
		runs    int
		want    []step
	}{
		{"quota", func(h *harness) {
			h.results = []runner.Result{{QuotaEnded: true, ErrorCode: runner.CodeQuota}}
			h.errs = []error{errors.New("worker failed")}
		}, 1, []step{{"infra", "quota"}, {"done", "none"}}},
		{"auth", func(h *harness) {
			h.lead("claude", mode.Edits, "high")
			h.results = []runner.Result{{AuthFailed: true, ErrorCode: runner.CodeAuth, Usage: runner.Usage{Input: 5, Output: 1}, UsageKnown: true}}
			h.auth.login = auth.LoggedIn
		}, 1, []step{{"failed", "auth"}, {"done", "none"}}},
		{"exit", func(h *harness) {
			h.errs = []error{fmt.Errorf("start: %w", exec.ErrNotFound)}
		}, 1, []step{{"failed", "exit"}, {"done", "none"}}},
		{"parse", func(h *harness) {
			h.results = []runner.Result{{ErrorCode: runner.CodeParse}}
			h.errs = []error{errors.New("worker failed")}
		}, 2, []step{{"failed", "parse"}, {"done", "none"}}},
	}
	for _, learning := range []bool{false, true} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/learning=%t", c.name, learning), func(t *testing.T) {
				h := newHarness(t)
				if learning {
					h.learning(true)
				}
				c.prepare(h)
				seen, opts := h.observed()
				for i := 0; i < c.runs; i++ {
					_, _ = h.runWith("implement", opts)
				}
				h.requireCalls(2)
				if len(*seen) != len(c.want) {
					t.Fatalf("attempts=%d want %d: %+v", len(*seen), len(c.want), *seen)
				}
				for i, a := range *seen {
					if a.Outcome != c.want[i].outcome || a.ErrorCode != c.want[i].code || a.Placement != "worker" || a.Role != "implement" || a.Agent != h.calls[i].Route.Agent {
						t.Fatalf("attempt %d: %+v want %+v", i, a, c.want[i])
					}
				}
				first, second := (*seen)[0], (*seen)[1]
				if first.Number != 1 {
					t.Fatalf("first attempt numbered %d", first.Number)
				}
				switch c.name {
				case "parse":
					if second.Number != 1 || second.Moved {
						t.Fatalf("a new delegation starts at 1: %+v", second)
					}
				case "auth":
					if second.Number != 2 || second.Reason != "auth" || second.Moved || first.Usage.Input != 5 || !first.UsageKnown {
						t.Fatalf("login retry: %+v then %+v", first, second)
					}
				default:
					if second.Number != 2 || !second.Moved || second.Reason != "availability" || second.Agent != "claude" || second.Family != "sonnet" {
						t.Fatalf("fallback: %+v", second)
					}
				}
			})
		}
	}
}

// A cancelled run is one interrupted attempt, and a retry that never ran is
// not counted: neither a login after the cancel nor a login that failed.
func TestAttemptCancellation(t *testing.T) {
	h := authHarness(t)
	h.results = []runner.Result{{Interrupted: true, AuthFailed: true}}
	h.errs = []error{context.Canceled}
	h.auth.login = auth.LoggedIn
	seen, opts := h.observed()
	if _, err := h.runWith("implement", opts); err == nil {
		t.Fatal("cancelled run succeeded")
	}
	h.requireCalls(1)
	if len(*seen) != 1 || (*seen)[0].Outcome != "interrupted" || (*seen)[0].ErrorCode != "none" || (*seen)[0].Number != 1 {
		t.Fatalf("%+v", *seen)
	}

	h = authHarness(t)
	h.results = []runner.Result{{AuthFailed: true, ErrorCode: runner.CodeAuth}}
	h.auth.login = auth.Declined
	seen, opts = h.observed()
	_, _ = h.runWith("implement", opts)
	h.requireCalls(1)
	if len(*seen) != 1 || (*seen)[0].Outcome != "failed" || (*seen)[0].ErrorCode != "auth" {
		t.Fatalf("a login that failed ran nothing more: %+v", *seen)
	}

	h = newHarness(t)
	h.results = []runner.Result{{Interrupted: true}}
	h.errs = []error{fmt.Errorf("worker failed: %w", context.DeadlineExceeded)}
	seen, opts = h.observed()
	_, _ = h.runWith("implement", opts)
	if len(*seen) != 1 || (*seen)[0].Outcome != "interrupted" || (*seen)[0].ErrorCode != "timeout" {
		t.Fatalf("timeout: %+v", *seen)
	}
}

// The runner sets the error code where the run failed and says whether the
// vendor reported usage; the delegate passes both on.
func TestAttemptRunnerCodes(t *testing.T) {
	answer := `{"done":true,"summary":"Done.","files":[],"needs":[]}`
	cases := []struct {
		name, agent string
		scenario    testbin.Scenario
		code        string
		usageKnown  bool
	}{
		{"claude done", "claude", testbin.Scenario{Stdout: `{"structured_output":` + answer + `,"usage":{"input_tokens":0,"output_tokens":0}}`}, runner.CodeNone, true},
		{"claude no usage", "claude", testbin.Scenario{Stdout: `{"structured_output":` + answer + `}`}, runner.CodeNone, false},
		{"claude parse", "claude", testbin.Scenario{Stdout: `{"result":"no structured output"}`}, runner.CodeParse, false},
		{"claude quota", "claude", testbin.Scenario{Exit: 1, Stderr: "usage limit"}, runner.CodeQuota, false},
		{"claude auth", "claude", testbin.Scenario{Exit: 1, Stderr: "Not logged in. Please run /login"}, runner.CodeAuth, false},
		{"claude exit", "claude", testbin.Scenario{Exit: 2, Stderr: "panic"}, runner.CodeExit, false},
		{"codex parse", "codex", testbin.Scenario{Answer: "no JSON here", Stdout: `{"type":"turn.completed","usage":{"input_tokens":3,"cached_input_tokens":1,"output_tokens":1}}` + "\n"}, runner.CodeParse, true},
		{"codex no answer", "codex", testbin.Scenario{}, runner.CodeParse, false},
		{"codex done", "codex", testbin.Scenario{Answer: answer}, runner.CodeNone, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := runner.Spec{TaskDir: t.TempDir(), Cwd: t.TempDir(), Stdin: []byte("task\n")}
			s.Route.Agent, s.Route.Model, s.Route.Sandbox = c.agent, "sonnet", "read-only"
			if c.agent == "codex" {
				s.Route.Model = "luna"
			}
			testbin.Install(t, c.agent, c.scenario)
			s.Env = []string{"PATH=" + os.Getenv("PATH")}
			r, err := runner.Run(context.Background(), s)
			if r.ErrorCode != c.code || r.UsageKnown != c.usageKnown || (c.code == runner.CodeNone) != (err == nil) {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

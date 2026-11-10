package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

const mcpTestLead = "codex/lead.lead-1"

// mcpAnswerBus is gateSetup as the lead's own process: no worker depth.
func mcpAnswerBus(t *testing.T) *bus.Bus {
	t.Helper()
	r, root := gateSetup(t, mode.Ask)
	t.Setenv("DUOLOG_DEPTH", "")
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r.Dir, cfg.Wire)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mcpAsk(t *testing.T, b *bus.Bus, to string, person bool, wait int) int64 {
	t.Helper()
	n, err := b.Send(bus.Message{From: "codex/ask.test", To: to, K: "ask", S: "exec custom", P: person, W: wait})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mcpAnswer(t *testing.T, decision string, n int64) (mcp.AnswerAskOutput, error) {
	t.Helper()
	return testDeps(t).mcpAnswerAsk(context.Background(), mcp.AnswerAskInput{Ask: n, Decision: decision})
}

func TestMCPAnswerAskAllowsOwnAsk(t *testing.T) {
	b := mcpAnswerBus(t)
	n := mcpAsk(t, b, mcpTestLead, false, 0)
	out, err := mcpAnswer(t, "allow", n)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ask " + strconv.FormatInt(n, 10) + " allowed"; out.Line != want {
		t.Fatalf("line = %q, want %q", out.Line, want)
	}
	lines, err := b.Inbox("codex/ask.test")
	if err != nil || len(lines) != 1 {
		t.Fatal(lines, err)
	}
	var m bus.Message
	if err := json.Unmarshal(lines[0], &m); err != nil {
		t.Fatal(err)
	}
	if m.From != mcpTestLead || m.K != "allow" || m.Ref != strconv.FormatInt(n, 10) {
		t.Fatalf("answer = %+v", m)
	}
}

func TestMCPAnswerAskDenyOwnAsk(t *testing.T) {
	b := mcpAnswerBus(t)
	n := mcpAsk(t, b, mcpTestLead, false, 0)
	out, err := mcpAnswer(t, "deny", n)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ask " + strconv.FormatInt(n, 10) + " denied"; out.Line != want {
		t.Fatalf("line = %q, want %q", out.Line, want)
	}
	lines, err := b.Inbox("codex/ask.test")
	if err != nil || len(lines) != 1 {
		t.Fatal(lines, err)
	}
	var m bus.Message
	if err := json.Unmarshal(lines[0], &m); err != nil || m.K != "deny" {
		t.Fatalf("answer = %+v %v", m, err)
	}
}

// The tool and `duolog allow <n>` give the same error for an ask they refuse.
func TestMCPAnswerAskSameErrorsAsCLI(t *testing.T) {
	b := mcpAnswerBus(t)
	d := testDeps(t)
	d.AgentAbove = func([]string) bool { return false }
	cases := []struct {
		name string
		ask  func() int64
		want string
	}{
		{"other lead", func() int64 { return mcpAsk(t, b, "codex/lead.lead-other", false, 0) }, "lead"},
		{"answered", func() int64 {
			n := mcpAsk(t, b, mcpTestLead, false, 0)
			if _, err := gateCommand(t, "", "deny", strconv.FormatInt(n, 10)); err != nil {
				t.Fatal(err)
			}
			return n
		}, "already answered"},
		{"expired", func() int64 {
			n := mcpAsk(t, b, mcpTestLead, false, 1)
			// Mutation killed: accepting an already expired ask in CLI or MCP.
			path := filepath.Join(b.Directory(), "bus.ndjson")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var m bus.Message
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Fatal(err)
				}
				if m.N == n {
					m.T = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
				}
				encoded, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				lines = append(lines, string(encoded))
			}
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			return n
		}, "expired"},
		{"unknown", func() int64 { return 99999 }, "unknown permission request"},
		{"person only", func() int64 {
			d.AgentAbove = func([]string) bool { return true }
			t.Cleanup(func() { d.AgentAbove = func([]string) bool { return false } })
			return mcpAsk(t, b, mcpTestLead, true, 0)
		}, "person"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cliAsk, toolAsk := tc.ask(), tc.ask()
			_, cliErr := gateCommand(t, "", "allow", strconv.FormatInt(cliAsk, 10))
			_, toolErr := mcpAnswer(t, "allow", toolAsk)
			if cliErr == nil || toolErr == nil {
				t.Fatalf("cli %v, tool %v: both must refuse", cliErr, toolErr)
			}
			if !strings.Contains(toolErr.Error(), tc.want) {
				t.Fatalf("tool error %q lacks %q", toolErr, tc.want)
			}
			// The ask number is the only difference between the two messages.
			cli := strings.ReplaceAll(cliErr.Error(), strconv.FormatInt(cliAsk, 10), "N")
			tool := strings.ReplaceAll(toolErr.Error(), strconv.FormatInt(toolAsk, 10), "N")
			if cli != tool {
				t.Fatalf("cli error %q, tool error %q", cliErr, toolErr)
			}
		})
	}
}

func TestMCPAnswerAskRejectsBadDecision(t *testing.T) {
	b := mcpAnswerBus(t)
	n := mcpAsk(t, b, mcpTestLead, false, 0)
	for _, decision := range []string{"", "maybe", "ALLOW"} {
		if _, err := mcpAnswer(t, decision, n); err == nil || !strings.Contains(err.Error(), "decision is allow or deny") {
			t.Errorf("decision %q: %v", decision, err)
		}
	}
	if _, err := mcpAnswer(t, "allow", 0); err == nil || !strings.Contains(err.Error(), "invalid request number") {
		t.Errorf("ask 0: %v", err)
	}
	if answered(t, ".", n) {
		t.Error("a refused call answered the ask")
	}
}

func TestMCPAnswerAskRefusesUnderWorker(t *testing.T) {
	b := mcpAnswerBus(t)
	n := mcpAsk(t, b, mcpTestLead, false, 0)
	t.Setenv("DUOLOG_DEPTH", "1")
	if _, err := mcpAnswer(t, "allow", n); err == nil || !strings.Contains(err.Error(), "workers cannot answer permission requests") {
		t.Fatal(err)
	}
}

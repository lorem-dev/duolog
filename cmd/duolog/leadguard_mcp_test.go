package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

// mcpAnswerCall is a lead's hook payload for one mcp__duolog__answer_ask
// call in the native mode native. A nil ask or decision leaves the field out.
func mcpAnswerCall(id, root, native string, ask, decision any) []byte {
	input := map[string]any{}
	if ask != nil {
		input["ask"] = ask
	}
	if decision != nil {
		input["decision"] = decision
	}
	data, _ := json.Marshal(map[string]any{"session_id": id, "cwd": root, "tool_name": answerAskTool, "permission_mode": native, "tool_input": input})
	return data
}

// ordinaryAsk puts an ask on the bus for lead-claude that the lead may
// answer: not person-only. It returns the ask's number.
func ordinaryAsk(t *testing.T, stateDir string) int64 {
	t.Helper()
	b, err := bus.Open(stateDir, config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	lead := session.Lead{ID: "lead-claude", Target: config.Target{Agent: "claude"}}
	n, err := b.Send(bus.Message{From: "duolog/ask.plain", To: session.Address(lead), K: policy.Ask, S: "read notes.txt", T: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A lead in ask mode that answers a person-only ask through the tool is asked
// exactly as for duolog allow in Bash, here and through the installed hook.
func TestGuardAsksPersonForPersonOnlyAskViaMCP(t *testing.T) {
	stateDir, root := leadTrustProject(t, "claude", "default")
	bash, bashOK := guardLeadCall(stateDir, "claude", modeInput("lead-claude", root, "default", "duolog allow 1"))
	d, ok := guardLeadCall(stateDir, "claude", mcpAnswerCall("lead-claude", root, "default", 1, "allow"))
	if !ok || d.Kind != policy.Ask || d.Rule != "person-only ask" {
		t.Fatalf("tool call: %+v, %v; want a person-only ask", d, ok)
	}
	if !bashOK || d.Reason != bash.Reason {
		t.Fatalf("tool reason %q, Bash reason %q (%v)", d.Reason, bash.Reason, bashOK)
	}
	t.Run("hook", func(t *testing.T) {
		isolatedRouteConfig(t)
		t.Chdir(root)
		out, err := gateCommand(t, string(mcpAnswerCall("lead-claude", root, "default", 1, "allow")), "gate", "--agent", "claude", "--event", "PreToolUse")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"permissionDecision":"ask"`) {
			t.Fatalf("hook did not ask: %s", out)
		}
	})
}

// A bypass lead that owns the person-only ask answers it through the tool as
// it would with duolog allow.
func TestGuardLetsBypassLeadAnswerOwnAskViaMCP(t *testing.T) {
	for _, native := range []string{"bypassPermissions"} {
		stateDir, root := leadTrustProject(t, "claude", native)
		for _, decision := range []string{"allow", "deny"} {
			if d, ok := guardLeadCall(stateDir, "claude", mcpAnswerCall("lead-claude", root, native, 1, decision)); ok {
				t.Errorf("%s lead, %s own ask: %+v", native, decision, d)
			}
		}
	}
}

// A bypass lead's allow of another lead's person-only ask through the tool
// gets no decision from the guard either; answer_ask itself refuses it
// (checkCaller). A default lead's is asked.
func TestGuardLeavesOtherLeadsAskViaMCPToBypassLead(t *testing.T) {
	stateDir, root := leadTrustProject(t, "claude", "bypassPermissions")
	n := otherLeadAsk(t, stateDir, "default")
	if d, ok := guardLeadCall(stateDir, "claude", mcpAnswerCall("lead-claude", root, "bypassPermissions", n, "allow")); ok {
		t.Fatalf("bypass, other lead's ask %d: %+v", n, d)
	}
	d, ok := guardLeadCall(stateDir, "claude", mcpAnswerCall("lead-claude", root, "default", n, "allow"))
	if !ok || d.Kind != policy.Ask || d.Rule != "person-only ask" {
		t.Fatalf("default, other lead's ask %d: %+v, %v", n, d, ok)
	}
}

// An ask that is not person-only gets no decision from the guard, in any mode.
func TestGuardIgnoresOrdinaryAskViaMCP(t *testing.T) {
	for _, native := range []string{"default", "bypassPermissions"} {
		stateDir, root := leadTrustProject(t, "claude", native)
		n := ordinaryAsk(t, stateDir)
		for _, decision := range []string{"allow", "deny"} {
			if d, ok := guardLeadCall(stateDir, "claude", mcpAnswerCall("lead-claude", root, native, n, decision)); ok {
				t.Errorf("%s lead, %s ordinary ask %d: %+v", native, decision, n, d)
			}
		}
	}
}

// An ask that is not a positive integer, or a decision other than allow or
// deny, may hide a person-only ask: with one pending, the guard asks.
func TestGuardTreatsBadAskNumberAsHidden(t *testing.T) {
	stateDir, root := leadTrustProject(t, "claude", "default")
	for _, tc := range []struct {
		name          string
		ask, decision any
	}{
		{"zero", 0, "allow"},
		{"negative", -1, "allow"},
		{"string", "1", "allow"},
		{"fraction", 1.5, "allow"},
		{"missing", nil, "allow"},
		{"object", map[string]int{"n": 1}, "allow"},
		{"zero deny", 0, "deny"},
		{"bad decision", 1, "maybe"},
		{"upper decision", 1, "ALLOW"},
		{"missing decision", 1, nil},
		{"number decision", 1, 1},
	} {
		d, ok := guardLeadCall(stateDir, "claude", mcpAnswerCall("lead-claude", root, "default", tc.ask, tc.decision))
		if !ok || d.Kind != policy.Ask || d.Rule != "person-only ask" {
			t.Errorf("%s: %+v, %v; want a person-only ask", tc.name, d, ok)
		}
	}
	// A tool_input that is no object at all is hidden too.
	data, _ := json.Marshal(map[string]any{"session_id": "lead-claude", "cwd": root, "tool_name": answerAskTool, "permission_mode": "default", "tool_input": "duolog allow 1"})
	if d, ok := guardLeadCall(stateDir, "claude", data); !ok || d.Kind != policy.Ask {
		t.Errorf("string tool_input: %+v, %v", d, ok)
	}
}

// Only duolog's answer_ask is rewritten: any other tool's input, and input
// that is not JSON, comes back byte for byte.
func TestGuardLeavesOtherMCPToolsAlone(t *testing.T) {
	root := t.TempDir()
	input := func(tool string) []byte {
		data, _ := json.Marshal(map[string]any{"session_id": "lead-claude", "cwd": root, "tool_name": tool, "permission_mode": "default", "tool_input": map[string]any{"ask": 1, "decision": "allow"}})
		return data
	}
	for _, in := range [][]byte{
		input("mcp__duolog__status"),
		input("mcp__duolog__peer"),
		input("mcp__other__x"),
		input("mcp__other__answer_ask"),
		modeInput("lead-claude", root, "default", "duolog allow 1"),
		[]byte("not json"),
		nil,
	} {
		if got := mcpAnswerInput(in); !bytes.Equal(got, in) {
			t.Errorf("input %s changed to %s", in, got)
		}
	}
}

// A Codex lead's answer_ask call for a person-only ask is not allowed: the
// guard denies it as it denies duolog allow 7 in Bash, here and through the
// installed hook. Codex's PreToolUse payload names the tool
// mcp__<server>__<tool> and carries no permission mode.
func TestCodexLeadAnswerAskGuarded(t *testing.T) {
	stateDir, root := leadTrustProject(t, "codex", "")
	for range 5 {
		ordinaryAsk(t, stateDir)
	}
	b, err := bus.Open(stateDir, config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
	if err != nil {
		t.Fatal(err)
	}
	lead := session.Lead{ID: "lead-codex", Target: config.Target{Agent: "codex"}}
	n, err := b.Send(bus.Message{From: "duolog/ask.person", To: session.Address(lead), K: policy.Ask, S: "read ../secret.txt", P: true, T: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil || n != 7 {
		t.Fatalf("ask %d, %v; want ask 7", n, err)
	}
	call, _ := json.Marshal(map[string]any{"session_id": "lead-codex", "cwd": root, "hook_event_name": "PreToolUse", "tool_name": "mcp__duolog__answer_ask", "tool_input": map[string]any{"ask": 7, "decision": "allow"}})
	bashCall, _ := json.Marshal(map[string]any{"session_id": "lead-codex", "cwd": root, "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]string{"command": "duolog allow 7"}})
	want, wantOK := guardLeadCall(stateDir, "codex", bashCall)
	got, ok := guardLeadCall(stateDir, "codex", call)
	if !ok || got.Kind != policy.Deny || got.Rule != "person-only ask" {
		t.Fatalf("tool call: %+v, %v; want a person-only deny", got, ok)
	}
	if got != want || ok != wantOK {
		t.Fatalf("tool %+v, %v; Bash %+v, %v", got, ok, want, wantOK)
	}
	t.Run("hook", func(t *testing.T) {
		isolatedRouteConfig(t)
		chdir(t, root)
		out, err := gateCommand(t, string(call), "gate", "--agent", "codex", "--event", "PreToolUse")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Fatalf("hook did not deny: %s", out)
		}
	})
}

// The tool call and the Bash line it stands for get the same decision from
// the guard, for every kind of ask and every lead mode.
func TestMCPAnswerInputMatchesBashDecision(t *testing.T) {
	for _, native := range []string{"default", "acceptEdits", "bypassPermissions", "auto"} {
		stateDir, root := leadTrustProject(t, "claude", native)
		other := otherLeadAsk(t, stateDir, "default")
		plain := ordinaryAsk(t, stateDir)
		for _, tc := range []struct {
			name          string
			ask, decision any
			bash          string
		}{
			{"own person-only allow", 1, "allow", "duolog allow 1"},
			{"own person-only deny", 1, "deny", "duolog deny 1"},
			{"other lead's allow", other, "allow", "duolog allow " + strconv.FormatInt(other, 10)},
			{"other lead's deny", other, "deny", "duolog deny " + strconv.FormatInt(other, 10)},
			{"ordinary allow", plain, "allow", "duolog allow " + strconv.FormatInt(plain, 10)},
			{"ordinary deny", plain, "deny", "duolog deny " + strconv.FormatInt(plain, 10)},
			{"unknown ask", 999, "allow", "duolog allow 999"},
			{"zero", 0, "allow", "duolog allow"},
			{"string", "1", "deny", "duolog allow"},
			{"bad decision", plain, "maybe", "duolog allow"},
		} {
			want, wantOK := guardLeadCall(stateDir, "claude", modeInput("lead-claude", root, native, tc.bash))
			got, gotOK := guardLeadCall(stateDir, "claude", mcpAnswerCall("lead-claude", root, native, tc.ask, tc.decision))
			if got != want || gotOK != wantOK {
				t.Errorf("%s lead, %s: tool %+v, %v; Bash %q %+v, %v", native, tc.name, got, gotOK, tc.bash, want, wantOK)
			}
		}
	}
}

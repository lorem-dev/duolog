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

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

// answerAsks replies kind to every ask that reaches the bus until the test
// ends. Without person it answers as the lead and leaves person-only asks
// alone, as duolog allow does for a lead; with person it answers those too.
func answerAsks(t *testing.T, root, kind string, person bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() { cancel(); <-done })
	dir := filepath.Join(root, ".duolog")
	go func() {
		defer close(done)
		answered := map[int64]bool{}
		for ctx.Err() == nil {
			time.Sleep(5 * time.Millisecond)
			data, err := os.ReadFile(filepath.Join(dir, "bus.ndjson"))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				var m bus.Message
				if json.Unmarshal([]byte(line), &m) != nil || m.K != policy.Ask || m.P && !person || answered[m.N] {
					continue
				}
				answered[m.N] = true
				reg, err := session.Open(dir)
				if err != nil {
					continue
				}
				lead, _, err := reg.Lead("lead-123456")
				if err != nil {
					continue
				}
				b, err := bus.Open(dir, config.Wire{MaxLineBytes: 512, MaxSummaryBytes: 200})
				if err == nil {
					_, _ = b.Send(bus.Message{From: session.Address(lead), To: m.From, K: kind, Ref: strconv.FormatInt(m.N, 10), S: kind})
				}
			}
		}
	}()
}

func busAsks(t *testing.T, root string) []bus.Message {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".duolog", "bus.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var asks []bus.Message
	for _, line := range strings.Split(string(data), "\n") {
		var m bus.Message
		if json.Unmarshal([]byte(line), &m) == nil && m.K == policy.Ask {
			asks = append(asks, m)
		}
	}
	return asks
}

func claudeGate(t *testing.T, event, toolUseID, command string) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"session_id": "claude-session", "tool_use_id": toolUseID, "tool_name": "Bash", "tool_input": map[string]string{"command": command}})
	out, err := gateCommand(t, string(data), "gate", "--agent", "claude", "--event", event)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Claude runs PreToolUse and then PermissionRequest for one tool call. Both
// reached the same ask, so the lead was asked twice for one command.
func TestGateOneAskForPreToolUseAndPermissionRequest(t *testing.T) {
	for _, kind := range []string{policy.Allow, policy.Deny} {
		t.Run(kind, func(t *testing.T) {
			_, root := gateSetup(t, mode.Ask)
			t.Setenv("DUOLOG_LEAD", "lead-123456")
			answerAsks(t, root, kind, false)
			pre := claudeGate(t, "PreToolUse", "toolu_1", "custom-command")
			perm := claudeGate(t, "PermissionRequest", "toolu_1", "custom-command")
			if n := len(busAsks(t, root)); n != 1 {
				t.Fatalf("%d asks on the bus for one tool call", n)
			}
			if kind == policy.Allow {
				if pre != "" || !strings.Contains(perm, `"behavior":"allow"`) {
					t.Fatalf("pre %q, permission %q", pre, perm)
				}
				return
			}
			if !strings.Contains(pre, `"permissionDecision":"deny"`) || !strings.Contains(perm, `"behavior":"deny"`) {
				t.Fatalf("pre %q, permission %q", pre, perm)
			}
		})
	}
}

// A Codex hook may carry no tool_use_id, so the ask record is keyed by the
// question. A patch of one file named "a ../x" in the work root asks the
// lead; a patch of a and ../x, outside it, asks the person, and its question
// reads the same. The lead's reply to the first answered the second.
func TestGateLeadReplyDoesNotAnswerPersonAsk(t *testing.T) {
	_, root := gateSetup(t, mode.Ask)
	answerAsks(t, root, policy.Allow, false)
	if out := codexPatch(t, root, "a ../x"); out != "" {
		t.Fatalf("the lead allowed the file a ../x, got %q", out)
	}
	if out := codexPatch(t, root, "a", "../x"); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("a write outside the work root took the lead's reply: %q", out)
	}
	asks := busAsks(t, root)
	if len(asks) != 2 || asks[0].P || !asks[1].P {
		t.Fatalf("want one lead ask and then one person ask, got %+v", asks)
	}
}

// codexPatch runs the Codex PreToolUse gate, with no tool_use_id, on a patch
// that adds files, and returns its output.
func codexPatch(t *testing.T, root string, files ...string) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"session_id": "codex-session", "cwd": root, "tool_name": "apply_patch",
		"tool_input": map[string]string{"command": "*** Begin Patch\n*** Add File: " + strings.Join(files, "\n+x\n*** Add File: ") + "\n+x\n*** End Patch\n"}})
	out, err := gateCommand(t, string(data), "gate", "--agent", "codex", "--event", "PreToolUse")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The ask record's key joined the asked files with spaces, so a patch of one
// file named "../a b" and a patch of ../a and b had one key. The person's allow
// for the first answered the second, which writes ../a, a file the person was
// never asked about.
func TestGateAskKeyKeepsFileNamesApart(t *testing.T) {
	_, root := gateSetup(t, mode.Ask)
	answerAsks(t, root, policy.Allow, true)
	codexPatch(t, root, "../a b")
	codexPatch(t, root, "../a", "b")
	asks := busAsks(t, root)
	if len(asks) != 2 || !asks[0].P || !asks[1].P {
		t.Fatalf("want one person ask for each patch, got %+v", asks)
	}
}

// A second tool call asks for itself, and a wait that ran out is not an answer.
func TestGateAskIsNotSharedAcrossCallsOrTimeouts(t *testing.T) {
	_, root := gateSetup(t, mode.Ask)
	shortAskWait(t)
	for _, id := range []string{"toolu_1", "toolu_1", "toolu_2"} {
		if out := claudeGate(t, "PermissionRequest", id, "custom-command"); !strings.Contains(out, `"behavior":"deny"`) {
			t.Fatal(out)
		}
	}
	if n := len(busAsks(t, root)); n != 3 {
		t.Fatalf("%d asks, want one per call that got no answer", n)
	}
}

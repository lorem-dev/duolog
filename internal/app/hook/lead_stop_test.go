package hook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
)

func stopReason(t *testing.T, out Output) string {
	t.Helper()
	var decision struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if out.Code != 0 || len(out.Stderr) != 0 || json.Unmarshal(out.Stdout, &decision) != nil || decision.Decision != "block" || decision.Reason == "" {
		t.Fatalf("want a Stop block with a reason, got %+v", out)
	}
	return decision.Reason
}

// Kills the mutation that omits working peers from the existing wait requirement.
func TestLeadStopRequiresWaitForOwnBusyPeer(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		for _, state := range []string{"starting", "working", "updating", "question"} {
			t.Run(agent+"/"+state, func(t *testing.T) {
				e, reg, _ := setup(t, "claude")
				SessionStart(context.Background(), e)
				if err := reg.Record(session.Entry{ID: "peer-busy", Lead: id(e), Agent: agent, Block: "block:peer"}); err != nil {
					t.Fatal(err)
				}
				e.Peers = func(_ context.Context, leadID string) []string {
					if leadID != id(e) {
						t.Fatalf("looked up lead %q, want %q", leadID, id(e))
					}
					return []string{"peer-busy " + state}
				}
				reason := stopReason(t, Stop(context.Background(), e))
				for _, want := range []string{"duolog peer wait peer-busy --lead " + id(e), "background", "--after-ask", "person"} {
					if !strings.Contains(reason, want) {
						t.Fatalf("reason %q lacks %q", reason, want)
					}
				}
				// A live peer wait allows the lead to yield to its notification.
				release, err := reg.Watch("peer-busy")
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				silent(t, Stop(context.Background(), e))
			})
		}
	}
}

func TestLeadStopAllowsSafeStops(t *testing.T) {
	for _, name := range []string{"ready", "closed", "exited", "gone", "interrupted", "limited", "other lead", "closed record", "retry", "subagent", "unknown lead", "codex lead", "cancelled", "no peers"} {
		t.Run(name, func(t *testing.T) {
			e, reg, _ := setup(t, "claude")
			SessionStart(context.Background(), e)
			entry := session.Entry{ID: "peer-busy", Lead: id(e), Agent: "claude", Block: "block:peer"}
			state := "starting"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "other lead":
				entry.Lead = "another-lead"
			case "closed record":
				entry.Closed = true
			case "retry":
				e = change(t, e, "stop_hook_active", true)
			case "subagent":
				e = change(t, e, "agent_id", "subagent-1")
			case "unknown lead":
				e = change(t, e, "session_id", "unknown-lead")
			case "codex lead":
				e = withAgent(e, "codex")
			case "cancelled":
				cancel()
			case "no peers":
			default:
				state = name
			}
			if err := reg.Record(entry); err != nil {
				t.Fatal(err)
			}
			if name != "no peers" {
				e.Peers = func(context.Context, string) []string { return []string{"peer-busy " + state} }
			}
			silent(t, Stop(ctx, e))
		})
	}
}

func TestLeadStopDeliversCompletionThatRacesWithStop(t *testing.T) {
	e, reg, b := setup(t, "claude")
	SessionStart(context.Background(), e)
	send(t, b, session.Address(lead(t, reg, e)), "peer finished")
	reason := stopReason(t, Stop(context.Background(), e))
	if !strings.Contains(reason, "peer finished") {
		t.Fatalf("completion lost at Stop: %q", reason)
	}
	silent(t, Stop(context.Background(), e))
}

// The restart note belongs to the prompt: a Stop neither delivers it nor moves
// the marker that would end it.
func TestLeadStopLeavesTheRestartNoteToThePrompt(t *testing.T) {
	e, _, _ := setup(t, "claude")
	var installed time.Time
	e.RestartNote = func(since time.Time) string {
		if !installed.IsZero() && since.Before(installed) {
			return "restart now"
		}
		return ""
	}
	SessionStart(context.Background(), e)
	ageMarker(t, e)
	installed = time.Now().Add(-time.Minute)
	silent(t, Stop(context.Background(), e))
	if out := Prompt(context.Background(), e); !strings.Contains(string(out.Stdout), "restart now") {
		t.Fatalf("the Stop used up the restart note: %s", out.Stdout)
	}
}

func TestLeadStopRequiresWaitAgainAfterWaitEnds(t *testing.T) {
	e, reg, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	if err := reg.Record(session.Entry{ID: "peer-busy", Lead: id(e), Block: "block:peer"}); err != nil {
		t.Fatal(err)
	}
	release, err := reg.Watch("peer-busy")
	if err != nil {
		t.Fatal(err)
	}
	release()
	e.Peers = func(context.Context, string) []string { return []string{"peer-busy starting"} }
	stopReason(t, Stop(context.Background(), e))
}

func TestLeadStopReturnsWithAHeldRegistryLock(t *testing.T) {
	e, reg, _ := setup(t, "claude")
	SessionStart(context.Background(), e)
	release := holdLock(t, reg.Dir, "sessions.lock")
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	stopReturns(t, ctx, e)
}

package gate

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
)

// asksOnly keeps the ask lines of the bus, without the lines that follow one.
func asksOnly(ms []bus.Message) []bus.Message {
	var asks []bus.Message
	for _, m := range ms {
		if m.K == Ask {
			asks = append(asks, m)
		}
	}
	return asks
}

// An ask that runs out of time is denied, and the lead learns of it in one
// line that names the request, the asker and the action.
func TestExpiredAskTellsTheLead(t *testing.T) {
	for _, tc := range []struct{ name, peer, role, from string }{
		{"peer", "peer-abc123", "", "peer-abc123"},
		{"worker", "", "implement", "worker implement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, dir := askSetup(t)
			t.Setenv("DUOLOG_PEER", tc.peer)
			t.Setenv("DUOLOG_ROLE", tc.role)
			d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: "make   lint"}, askSendWait, false)
			if d.Kind != Deny || !strings.Contains(d.Reason, "timeout") {
				t.Fatal(d)
			}
			ms := askMessages(t, dir)
			if len(ms) != 2 || ms[0].K != Ask {
				t.Fatalf("want the ask and one expiry line: %+v", ms)
			}
			ask, note := ms[0], ms[1]
			want := "ask " + strconv.FormatInt(ask.N, 10) + " from " + tc.from + " expired and was denied: exec make lint"
			if note.S != want || note.To != ask.To || note.K != "status" || note.Ref != strconv.FormatInt(ask.N, 10) {
				t.Fatalf("expiry line %+v, want %q to %s", note, want, ask.To)
			}
		})
	}
}

// An answered ask, or one the worker gave up on, sends no expiry line.
func TestAnsweredOrCancelledAskSendsNoExpiry(t *testing.T) {
	askReply(t, Allow, "codex/lead.first-", false, Allow)
	b, _, dir := askSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for len(askMessages(t, dir)) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	if d := Escalate(ctx, b, "first-123456", Action{Kind: "exec", Target: "make lint"}, time.Minute, false); d.Kind != Deny {
		t.Fatal(d)
	}
	if ms := askMessages(t, dir); len(ms) != 1 {
		t.Fatalf("a cancelled ask did not expire: %+v", ms)
	}
}

// A long action is cut so that the expiry line still fits the wire limits.
func TestExpiryLineFitsTheWire(t *testing.T) {
	b, _, dir := askSetup(t)
	t.Setenv("DUOLOG_PEER", "peer-abc123")
	Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: strings.Repeat("界x", 300)}, askSendWait, false)
	ms := askMessages(t, dir)
	if len(ms) != 2 || len(ms[1].S) > 200 || !strings.HasPrefix(ms[1].S, "ask ") || !strings.Contains(ms[1].S, "[cut") {
		t.Fatalf("%+v", ms)
	}
}

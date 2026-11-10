package gate

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
)

// answerAsk replies to the nth ask on the bus, counted from 1, as the lead does.
func answerAsk(t *testing.T, b *bus.Bus, r *session.Registry, dir, kind string, nth int) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var asks []bus.Message
		if _, err := os.Stat(filepath.Join(dir, "bus.ndjson")); err == nil {
			for _, m := range askMessages(t, dir) {
				if m.K == Ask {
					asks = append(asks, m)
				}
			}
		}
		if len(asks) >= nth {
			ask := asks[nth-1]
			lead, _, err := r.Lead("first-123456")
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := b.Send(bus.Message{From: session.Address(lead), To: ask.From, K: kind, Ref: strconv.FormatInt(ask.N, 10), S: kind}); err != nil {
				t.Error(err)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("no ask sent")
}

func TestTelemetryGateAskResult(t *testing.T) {
	action := Action{Kind: "exec", Target: "custom-command --token=secret", Role: "implement"}
	for _, tc := range []struct {
		name, reply string
		person      bool
		prepare     func(t *testing.T, r *session.Registry)
		wantKind    string
		want        AskResult
	}{
		{name: "lead allows", reply: Allow, wantKind: Allow, want: AskResult{Answer: "allow", By: "lead"}},
		{name: "person denies", reply: Deny, person: true, wantKind: Deny, want: AskResult{Answer: "deny", By: "person"}},
		{name: "timeout", wantKind: Deny, want: AskResult{Answer: "timeout", By: "lead"}},
		{name: "person timeout", person: true, wantKind: Deny, want: AskResult{Answer: "timeout", By: "person"}},
		{name: "lead waits", wantKind: Deny, want: AskResult{Answer: "deny", By: "auto"}, prepare: func(t *testing.T, r *session.Registry) {
			if err := r.SetWaiting("first-123456", "foreground", os.Getpid()); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "no spawning lead", wantKind: Deny, want: AskResult{Answer: "deny", By: "auto"}, prepare: func(t *testing.T, _ *session.Registry) {
			t.Setenv("DUOLOG_LEAD", "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, r, dir := askSetup(t)
			if tc.prepare != nil {
				tc.prepare(t, r)
			}
			var got []AskResult
			ctx := WithAskObserver(context.Background(), func(a AskResult) { got = append(got, a) })
			done := make(chan struct{})
			go func() {
				defer close(done)
				if tc.reply != "" {
					answerAsk(t, b, r, dir, tc.reply, 1)
				}
			}()
			started := time.Now()
			wait := askSendWait
			if tc.reply != "" {
				// A reply exercises the observer, not a race against the
				// runner's filesystem and the default 500 ms poll interval.
				wait = 10 * time.Second
			}
			d := Escalate(ctx, b, "first-123456", action, wait, tc.person)
			<-done
			if d.Kind != tc.wantKind || d.Rule != "ask lead" {
				t.Fatalf("decision changed: %+v", d)
			}
			if len(got) != 1 {
				t.Fatalf("results: %+v", got)
			}
			if got[0].Answer != tc.want.Answer || got[0].By != tc.want.By {
				t.Fatalf("result %+v, want %+v", got[0], tc.want)
			}
			if got[0].Wait < 0 || got[0].Wait > time.Since(started) {
				t.Fatalf("wait %s outside the escalation", got[0].Wait)
			}
			if tc.want.Answer == "timeout" && got[0].Wait < wait {
				t.Fatalf("a timeout waited only %s", got[0].Wait)
			}
		})
	}
}

// A second Escalate in a context that already reported its ask, as a hook that
// runs again for the same call, reports nothing more.
func TestTelemetryGateAskReentry(t *testing.T) {
	b, r, dir := askSetup(t)
	n := 0
	ctx := WithAskObserver(context.Background(), func(AskResult) { n++ })
	for call := 1; call <= 2; call++ {
		done := make(chan struct{})
		go func() { defer close(done); answerAsk(t, b, r, dir, Allow, call) }()
		if d := Escalate(ctx, b, "first-123456", Action{Kind: "write", Target: "a.go", Role: "implement"}, 10*time.Second, false); d.Kind != Allow {
			t.Fatal(d)
		}
		<-done
	}
	if n != 1 {
		t.Fatalf("%d results for one hook call", n)
	}
}

// Without an observer Escalate decides as before, and a nil observer is none.
func TestTelemetryGateAskWithoutObserver(t *testing.T) {
	b, _, _ := askSetup(t)
	ctx := WithAskObserver(context.Background(), nil)
	if d := Escalate(ctx, b, "first-123456", Action{Kind: "write", Target: "a.go", Role: "implement"}, askSendWait, false); d.Kind != Deny {
		t.Fatal(d)
	}
}

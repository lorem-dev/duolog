package peer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/bus"
)

func TestWaitAfterAskKeepsWatchUntilAnswerOrExpiry(t *testing.T) {
	for _, action := range []string{"allow", "deny", "expire", "new ask", "cancel"} {
		t.Run(action, func(t *testing.T) {
			r := newRig(t, baseScript())
			peer := r.open("implement")
			b, err := bus.Open(r.reg.Dir, r.cfg.Wire)
			if err != nil {
				t.Fatal(err)
			}
			address := "codex/lead.lead-1"
			ask, err := b.Send(bus.Message{From: "duolog/ask.pending", To: address, K: "ask", Ref: peer.ID, S: "person approval", P: true, W: 120})
			if err != nil {
				t.Fatal(err)
			}
			before, sleeps := len(r.calls()), 0
			var cancel context.CancelFunc
			r.useDeps(func(d *Deps) {
				d.Clock = funcClock(func(ctx context.Context, _ time.Duration) error {
					sleeps++
					if sleeps != 1 {
						return ctx.Err()
					}
					if watched, err := r.reg.Watched(peer.ID); err != nil || !watched {
						t.Fatalf("no live watch while awaiting ask: %v %v", watched, err)
					}
					if len(r.calls()) != before {
						t.Fatal("captured peer before its pending gate ask settled")
					}
					switch action {
					case "allow", "deny":
						// The person's reply, not the wait, decides the ask.
						_, err = b.Send(bus.Message{From: address, To: "duolog/ask.pending", K: action, Ref: strconv.FormatInt(ask, 10), S: "person answered"})
					case "expire":
						expireBusAsk(t, r.reg.Dir, ask)
					case "new ask":
						_, err = b.Send(bus.Message{From: "duolog/ask.next", To: address, K: "ask", Ref: peer.ID, S: "another approval", W: 120})
					case "cancel":
						cancel()
					}
					if err != nil {
						t.Fatal(err)
					}
					return ctx.Err()
				})
			})
			ctx, done := context.WithTimeout(r.ctx, 5*time.Second)
			cancel = done
			defer done()
			got, err := WaitAfterAsk(ctx, peer.ID, ask)
			if sleeps == 0 {
				t.Fatalf("returned without waiting for the relayed ask: %+v %v", got, err)
			}
			switch action {
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("want cancellation, got %+v %v", got, err)
				}
			case "new ask":
				if err != nil || got.State != Question || !strings.Contains(got.Question, "another approval") {
					t.Fatalf("new ask was not reported: %+v %v", got, err)
				}
			default:
				if err != nil || got.State != Ready {
					t.Fatalf("did not resume after ask settled: %+v %v", got, err)
				}
			}
			if watched, err := r.reg.Watched(peer.ID); err != nil || watched {
				t.Fatalf("wait marker left behind: %v %v", watched, err)
			}
			// Waiting must not consume the lead's bus cursor or answer an ask.
			lines, err := b.Inbox(address)
			if err != nil || len(lines) == 0 {
				t.Fatalf("wait consumed the ask: %v", err)
			}
		})
	}
}

func expireBusAsk(t *testing.T, dir string, ask int64) {
	t.Helper()
	path := filepath.Join(dir, "bus.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var m bus.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		if m.N == ask {
			m.T = time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
		}
		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, encoded...)
		out = append(out, '\n')
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

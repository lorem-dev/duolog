package gate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
)

// A reply-poll lock waits for the ask's deadline, not a shorter notice
// budget. Contention that ends before that deadline preserves an allow.
func TestAskPollAllowsAfterLockContention(t *testing.T) {
	for _, name := range []string{"bus.lock", "sessions.lock"} {
		t.Run(name, func(t *testing.T) {
			b, r, dir := askSetup(t)
			lead, _, err := r.Lead("first-123456")
			if err != nil {
				t.Fatal(err)
			}
			const from = "duolog/ask.test"
			address := session.Address(lead)
			if _, err := b.Send(bus.Message{From: from, To: address, K: Ask}); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Send(bus.Message{From: address, To: from, K: Allow, Ref: "1"}); err != nil {
				t.Fatal(err)
			}
			lock := flock.New(filepath.Join(dir, name))
			if err := lock.Lock(); err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			poll := askPoll{reg: r.Within(ctx), bus: b.Within(ctx), lead: lead.ID, from: from, address: address, n: 1}
			done := make(chan struct{})
			var answer string
			var pollErr error
			go func() {
				defer close(done)
				answer, pollErr = poll.answer()
			}()
			select {
			case <-done:
				t.Fatalf("poll ended while the lock was held: %q, %v", answer, pollErr)
			case <-time.After(2 * store.NoticeWait):
			}
			if err := lock.Unlock(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
				if pollErr != nil || answer != Allow {
					t.Fatalf("poll = %q, %v; want allow", answer, pollErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("poll did not resume after the lock was released")
			}
		})
	}
}

// holdLock takes the named lock of the state folder, as another process that
// stalls with it would, until the test ends.
func holdLock(t *testing.T, dir, name string) {
	t.Helper()
	l := flock.New(filepath.Join(dir, name))
	if err := l.Lock(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
}

// A bus or registry lock that another process holds once the ask is out
// does not hold back the deny: the wait ends on time, and the expiry line,
// which is best effort, is dropped.
func TestExpiryDenyReturnsWithAHeldLock(t *testing.T) {
	for _, name := range []string{"bus.lock", "sessions.lock"} {
		t.Run(name, func(t *testing.T) {
			b, _, dir := askSetup(t)
			done := make(chan Decision, 1)
			go func() {
				done <- Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: "make lint"}, askSendWait, false)
			}()
			for deadline := time.Now().Add(askSendWait); len(asksOnly(askMessages(t, dir))) == 0; time.Sleep(5 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the ask was not sent")
				}
			}
			holdLock(t, dir, name)
			select {
			case d := <-done:
				if d.Kind != Deny || !strings.Contains(d.Reason, "timeout") {
					t.Fatalf("decision %+v, want the timeout deny", d)
				}
			case <-time.After(askSendWait + 3*time.Second):
				t.Fatal("the deny waited for the held lock")
			}
		})
	}
}

// An expiry line whose wait for the bus lock is cancelled ends at once.
func TestExpiryNoticeEndsWhenCancelled(t *testing.T) {
	b, _, dir := askSetup(t)
	saved := store.NoticeWait
	store.NoticeWait = time.Minute
	t.Cleanup(func() { store.NoticeWait = saved })
	holdLock(t, dir, "bus.lock")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	want := Decision{Kind: Deny, Reason: "ask timeout or cancellation"}
	done := make(chan Decision, 1)
	go func() {
		done <- expire(ctx, b, bus.Message{From: "duolog/ask.x", To: "codex/lead.first-"}, 1, "exec make lint", want)
	}()
	select {
	case d := <-done:
		if d != want {
			t.Fatalf("decision %+v, want %+v", d, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expire waited for the held lock after its cancellation")
	}
}

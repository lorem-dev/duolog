package gate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/store"
)

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

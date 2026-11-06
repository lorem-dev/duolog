package peer

import (
	"context"
	"testing"
	"time"
)

// peer wait marks its peer as watched while it runs, so that the peer's Stop
// hook leaves the report of the turn to the wait; the mark goes with the wait.
func TestWaitMarksItsPeerWatched(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.open("implement")
	var during []bool
	r.useDeps(func(d *Deps) {
		d.Clock = funcClock(func(ctx context.Context, _ time.Duration) error {
			watched, err := r.reg.Watched(e.ID)
			if err != nil {
				t.Error(err)
			}
			during = append(during, watched)
			return ctx.Err()
		})
	})
	if watched, _ := r.reg.Watched(e.ID); watched {
		t.Fatal("watched before any wait")
	}
	if _, err := Wait(r.ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if len(during) == 0 || !during[0] {
		t.Fatalf("not watched during the wait: %v", during)
	}
	if watched, err := r.reg.Watched(e.ID); err != nil || watched {
		t.Fatalf("still watched after the wait: %v %v", watched, err)
	}
}

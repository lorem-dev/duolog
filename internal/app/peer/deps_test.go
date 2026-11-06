package peer

import (
	"context"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/session"
)

// TestPeerOpenUsesDeps: an open reaches the model lookup, the record and the
// env file hook of Deps once each, and its clock is never asked to sleep for
// longer than Pauses.Poll. The Pane port moved to a later task, so rex is
// still the fake binary of the rig.
func TestPeerOpenUsesDeps(t *testing.T) {
	r := newRig(t, baseScript())
	var longest time.Duration
	var resolved, recorded, envFiles int
	r.useDeps(func(d *Deps) {
		d.Pauses = Pauses{Poll: 10 * time.Millisecond, Settle: time.Millisecond, Send: 2 * time.Millisecond, Key: time.Millisecond}
		d.Clock = funcClock(func(ctx context.Context, p time.Duration) error {
			longest = max(longest, p)
			return ctx.Err()
		})
		resolve := d.ResolveModel
		d.ResolveModel = func(ctx context.Context, cfg *config.Config, dir string, target config.Target) (string, error) {
			resolved++
			return resolve(ctx, cfg, dir, target)
		}
		d.RecordPeer = func(reg *session.Registry, e session.Entry, limit int) error {
			recorded++
			return reg.RecordLimited(e, limit)
		}
		d.OnEnvFile = func(string) { envFiles++ }
	})
	r.open("implement")
	if resolved != 1 || recorded != 1 || envFiles != 1 {
		t.Fatalf("model lookups %d, records %d, env file hooks %d; want one each", resolved, recorded, envFiles)
	}
	if longest > 10*time.Millisecond {
		t.Fatalf("slept %v, more than Pauses.Poll", longest)
	}
}

package peer

import (
	"context"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// TestPeerOpenUsesDeps: an open reaches the model lookup, the record, the env
// file hook and the Pane's Open of Deps once each, and its clock is never
// asked to sleep for longer than Pauses.Poll.
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
		counted := map[string]ModelResolver{}
		for agent, m := range d.Models {
			counted[agent] = resolveFunc(func(ctx context.Context, cfg *config.Config, dir string, target config.Target) (string, error) {
				resolved++
				return m.Resolve(ctx, cfg, dir, target)
			})
		}
		d.Models = counted
		d.RecordPeer = func(reg *session.Registry, e session.Entry, limit int) error {
			recorded++
			return reg.RecordLimited(e, limit)
		}
		d.OnEnvFile = func(string) { envFiles++ }
	})
	r.open("implement")
	if resolved != 1 || recorded != 1 || envFiles != 1 || len(r.spawns()) != 1 {
		t.Fatalf("model lookups %d, records %d, env file hooks %d, pane opens %v; want one each", resolved, recorded, envFiles, r.spawns())
	}
	if longest > 10*time.Millisecond {
		t.Fatalf("slept %v, more than Pauses.Poll", longest)
	}
}

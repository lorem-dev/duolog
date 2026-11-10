package paidruns

import (
	"reflect"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

// Kills feeding worker or peer stream windows into the admission quota cache.
func TestFinishDoesNotCacheStreamWindows(t *testing.T) {
	for _, shape := range []launch.Shape{launch.Worker, launch.Peer} {
		t.Run(string(shape), func(t *testing.T) {
			w := newGateWorld(t)
			req := w.request("claude", shape, launch.Free, 100)
			req.OpenPeer = shape == launch.Peer
			p := w.acquire(req)
			w.now = w.now.Add(time.Second)
			e := account.RunEvidence{Expected: true, Seen: true,
				Windows: []account.Window{{Kind: "weekly_all", DurationMinutes: 10080, UsedPercent: 9, ResetsAt: w.now.Add(72 * time.Hour)}}}
			w.finish(p, paidrules.Settlement{Started: true, CompleteUsage: true, Input: 1_000_000, Evidence: e})
			s, ok := w.gate.Cache.Latest("claude")
			if !ok || !reflect.DeepEqual(s, p.Snapshot) || w.reads != 1 || len(w.state().Samples) != 0 {
				t.Fatalf("snapshot %+v, want admission %+v; reads %d samples %+v", s, p.Snapshot, w.reads, w.state().Samples)
			}
		})
	}
}

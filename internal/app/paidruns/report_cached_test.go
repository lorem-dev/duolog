package paidruns

import (
	"context"
	"testing"

	"github.com/lorem-dev/duolog/internal/workerenv"
)

// ReportCached reads no quota: without a read in the cache the assessment is
// still the agent's, and once Report has read, ReportCached judges from that
// read and gives the same verdict and session numbers.
func TestReportCachedReadsNothing(t *testing.T) {
	ctx := context.Background()
	w := newGateWorld(t)
	in := w.request("claude", workerenv.Worker, workerenv.Free, 100).Input
	cold, _ := w.gate.ReportCached(ctx, in)
	if w.reads != 0 || cold.Assessment.Agent != "claude" {
		t.Fatalf("reads %d, report %+v", w.reads, cold.Assessment)
	}
	live, err := w.gate.Report(ctx, in)
	if err != nil || w.reads != 1 {
		t.Fatalf("reads %d: %v", w.reads, err)
	}
	cached, err := w.gate.ReportCached(ctx, in)
	if err != nil || w.reads != 1 {
		t.Fatalf("reads %d: %v", w.reads, err)
	}
	c, l := cached.Sessions, live.Sessions
	if cached.Assessment.Verdict != live.Assessment.Verdict || c.Tier != l.Tier || c.Limit != l.Limit || c.Live != l.Live {
		t.Fatalf("cached %+v %+v, live %+v %+v", cached.Assessment, cached.Sessions, live.Assessment, live.Sessions)
	}
}

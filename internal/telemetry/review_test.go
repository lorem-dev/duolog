package telemetry

import (
	"errors"
	"slices"
	"testing"
)

func TestRollbackSegmentsSurviveReadAndCompressionRetry(t *testing.T) {
	// Mutations killed: preferring gzip hides new plain events; retrying a merged gzip duplicates plain events.
	const yesterday = "2026-11-04"
	s, root := readFixture(t)
	plain := accountSegment(root, yesterday)
	plant(t, plain+".gz", gzipped(t, quotaLine(t, 1, testNow.Add(-day))))
	plant(t, plain, quotaLine(t, 2, testNow.Add(-day)))
	if got := epochs(t, mustRead(t, s, Query{})); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("before compression: %v", got)
	}
	setFaultHook(t, func(step string) error {
		if step == "rename" {
			return errors.New("crash")
		}
		return nil
	})
	mustPrune(t, s, true)
	faultHook = nil
	if got := epochs(t, mustRead(t, s, Query{})); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("after crash: %v", got)
	}
	mustPrune(t, s, true)
	if got := epochs(t, mustRead(t, s, Query{})); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("after retry: %v", got)
	}
}

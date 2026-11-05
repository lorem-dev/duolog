package telemetry

import (
	"sync/atomic"
	"time"
)

// Health is a best-effort count of this process's lost events since Since,
// the process start. It lives in memory only and is never a durable total:
// status and doctor label it "since process start".
type Health struct {
	// Dropped counts events lost after collection accepted them: every
	// failure, plus events refused by a cap.
	Dropped uint64
	// Failures counts appends that failed.
	Failures uint64
	Since    time.Time
}

var (
	processStart      = time.Now()
	dropped, failures atomic.Uint64
)

// countFailure records one failed append, which also drops its event.
func countFailure() {
	failures.Add(1)
	dropped.Add(1)
}

// Health returns the counters of this process; a nil Store reads them too,
// so a caller whose Open failed can still report them.
func (*Store) Health() Health {
	return Health{Dropped: dropped.Load(), Failures: failures.Load(), Since: processStart}
}

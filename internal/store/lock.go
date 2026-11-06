package store

import (
	"context"
	"time"

	"github.com/gofrs/flock"
)

// NoticeWait is how long a best-effort notice, such as a peer's stop line or
// an ask's expiry line, waits for a state lock before it is dropped. Tests
// change it.
var NoticeWait = 300 * time.Millisecond

// Lock takes lock. A nil ctx waits as long as it takes. Otherwise the lock
// is tried again every few milliseconds until ctx ends, and then ctx's error
// is returned, so that a process that stalls with the lock cannot hold the
// caller.
func Lock(ctx context.Context, lock *flock.Flock) error {
	if ctx == nil {
		return lock.Lock()
	}
	locked, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return context.DeadlineExceeded
	}
	return nil
}

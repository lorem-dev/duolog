package store

import (
	"context"
	"errors"
	"sync"
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

// ErrLockUnavailable is what LockFileContext returns when its lock could not
// be taken and the lock library gave no other reason.
var ErrLockUnavailable = errors.New("lock unavailable")

// LockFile takes the lock file at path and waits as long as it takes. The
// returned unlock releases it and may be called more than once; only the
// first call acts, and later calls return nil.
func LockFile(path string) (unlock func() error, err error) {
	l := flock.New(path)
	if err := l.Lock(); err != nil {
		_ = l.Close()
		return nil, err
	}
	return releaser(l), nil
}

// TryLockFile takes the lock file at path once. When another holder has it,
// ok is false and unlock is nil.
func TryLockFile(path string) (unlock func() error, ok bool, err error) {
	l := flock.New(path)
	locked, err := l.TryLock()
	if err != nil || !locked {
		_ = l.Close()
		return nil, false, err
	}
	return releaser(l), true, nil
}

// LockFileContext takes the lock file at path, trying again every retry
// until ctx ends. It returns ctx's error then.
func LockFileContext(ctx context.Context, path string, retry time.Duration) (unlock func() error, err error) {
	l := flock.New(path)
	locked, err := l.TryLockContext(ctx, retry)
	if err != nil || !locked {
		_ = l.Close()
		if err == nil {
			err = ErrLockUnavailable
		}
		return nil, err
	}
	return releaser(l), nil
}

func releaser(l *flock.Flock) func() error {
	var once sync.Once
	return func() (err error) {
		once.Do(func() { err = l.Close() })
		return err
	}
}

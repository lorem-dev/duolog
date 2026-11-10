package paidstore

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/store"
)

const (
	readsFile    = "quota-reads.json"
	readsLock    = "quota-reads.lock"
	readsVersion = 1
	// readsLockWait bounds the wait for the file's lock. The file only spares
	// a passing failure, so a lock that stays taken costs that and no more.
	readsLockWait = time.Second
)

// recentReads is the file of good reads by CacheKey. A snapshot holds names
// and numbers only, and the keys are hashes.
type recentReads struct {
	Version int                         `json:"version"`
	Reads   map[string]account.Snapshot `json:"reads"`
}

// loadReads returns the reads in the file. A missing, broken or foreign file
// holds none. The file is replaced whole, so no lock is needed to read it.
func (c *Cache) loadReads() map[string]account.Snapshot {
	data, err := store.ReadFile(filepath.Join(c.Dir, readsFile))
	if err != nil {
		return nil
	}
	var file recentReads
	if json.Unmarshal(data, &file) != nil || file.Version != readsVersion {
		return nil
	}
	return file.Reads
}

// readOrRecall reads p now, and keeps to a recent good read when the read
// fails as recall allows.
func (c *Cache) readOrRecall(ctx context.Context, p launch.Process, readNow bool, maxAge time.Duration) (account.Snapshot, error) {
	s, err := c.readWithProbeFallback(ctx, p, maxAge)
	if recent, ok := c.recall(ctx, p.CacheKey, readNow, err, maxAge); ok {
		return recent, nil
	}
	return s, err
}

// readWithProbeFallback still reads /usage and auth now. Only the model
// probe may use retained windows, including for a requested fresh read.
func (c *Cache) readWithProbeFallback(ctx context.Context, p launch.Process, maxAge time.Duration) (account.Snapshot, error) {
	if p.Agent == "claude" && c.Dir != "" {
		ctx = quota.WithClaudeProbeFallback(ctx, func() (account.Snapshot, bool) {
			s, ok := c.loadReads()[p.CacheKey]
			return s, ok && within(s.ReadAt, c.now(), maxAge)
		})
	}
	return c.Read(ctx, p)
}

// recall returns the kept read of key that stands in for a read that just
// failed: only for a transient failure of a caller that is still waiting and
// did not ask for a read made now, and only within maxAge of that read.
func (c *Cache) recall(ctx context.Context, key string, fresh bool, readErr error, maxAge time.Duration) (account.Snapshot, bool) {
	if c.Dir == "" || fresh || ctx.Err() != nil || !account.IsTransient(readErr) {
		return account.Snapshot{}, false
	}
	s, ok := c.loadReads()[key]
	if !ok || !within(s.ReadAt, c.now(), maxAge) {
		return account.Snapshot{}, false
	}
	s.Recalled = true
	return s, true
}

// remember keeps a read made now for the next duolog process: a successful
// one replaces the key's read, whatever it says, and a definite failure
// forgets it. A transient failure leaves it, and a recalled read is no read
// made now. Any problem with the file only costs that fallback, so remember
// reports none.
func (c *Cache) remember(key string, s account.Snapshot, readErr error, maxAge time.Duration) {
	if c.Dir == "" || s.Recalled || account.IsTransient(readErr) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), readsLockWait)
	defer cancel()
	unlock, err := store.LockFileContext(ctx, filepath.Join(c.Dir, readsLock), 10*time.Millisecond)
	if err != nil {
		return
	}
	defer func() { _ = unlock() }()
	before := c.loadReads()
	reads := maps.Clone(before)
	if reads == nil {
		reads = map[string]account.Snapshot{}
	}
	now := c.now()
	maps.DeleteFunc(reads, func(_ string, kept account.Snapshot) bool { return !within(kept.ReadAt, now, maxAge) })
	if readErr != nil {
		delete(reads, key)
	} else {
		reads[key] = s
	}
	if len(before) == 0 && len(reads) == 0 {
		return
	}
	c.writeReads(reads)
}

// writeReads replaces the file through a private temporary one, so a reader
// never sees half a file and a failed write keeps the previous one.
func (c *Cache) writeReads(reads map[string]account.Snapshot) {
	data, err := json.Marshal(recentReads{Version: readsVersion, Reads: reads})
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(c.Dir, "."+readsFile+"-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	if tmp.Close() != nil || writeErr != nil || os.Chmod(tmp.Name(), 0o600) != nil {
		return
	}
	_ = store.Rename(tmp.Name(), filepath.Join(c.Dir, readsFile))
}

// within reports whether a read made at readAt is no older than maxAge at now.
// A read without a time, or from the future, is not.
func within(readAt, now time.Time, maxAge time.Duration) bool {
	age := now.Sub(readAt)
	return !readAt.IsZero() && age >= 0 && age <= maxAge
}

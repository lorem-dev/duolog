package paidruns

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// A read that times out, on a machine too loaded to start the CLI in time,
// may borrow the last good read of the same account and route while that
// read is young and far from every limit. Any other failed read stays paid.
const (
	// lendMaxAge is the age of the oldest read lent.
	lendMaxAge = 10 * time.Minute
	// lendMaxUsed is the used percent that every window of a lent read must
	// stay under. In lendMaxAge a five-hour window would have to move by more
	// than 20 points, two a minute, to reach its limit: a pace that uses the
	// whole window in under an hour, five times the pace that just fills it.
	// The weekly window moves slower still, and admission adds the task's own
	// estimate and the reserves of tasks in flight on top of the lent read.
	lendMaxUsed = 80.0
)

// LastRead is the last good read of one route, kept in the account state:
// the account key that read recorded and its snapshot, names and numbers
// only.
type LastRead struct {
	Account  string         `json:"account"`
	Snapshot quota.Snapshot `json:"snapshot"`
}

// lendable reports whether a lent read s may decide a verdict at now: it is
// no older than lendMaxAge, healthy, and every window, the model families'
// included, is under lendMaxUsed.
func lendable(s quota.Snapshot, now time.Time) bool {
	age := now.Sub(s.ReadAt)
	if s.ReadAt.IsZero() || age < 0 || age > lendMaxAge || !s.Healthy(now) {
		return false
	}
	near := func(w quota.Window) bool { return w.UsedPercent >= lendMaxUsed }
	if slices.ContainsFunc(s.Windows, near) {
		return false
	}
	for _, windows := range s.ModelLimits {
		if slices.ContainsFunc(windows, near) {
			return false
		}
	}
	return true
}

// teaches reports whether s, the read a run was admitted on, may start a
// window rate interval: a real read, not a lent one.
func teaches(s quota.Snapshot) bool {
	return !s.ReadAt.IsZero() && !s.Cached
}

// read reads the quota p sees. A successful read on p's own provider
// env_key is kept as the route's last good read; a read that timed out may
// borrow one, see lend.
func (c *Cache) read(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
	s, err := c.Read(ctx, p)
	if err != nil {
		if last, ok := c.lend(ctx, p, err); ok {
			return last, nil
		}
		return s, err
	}
	if key := s.Billing.ProviderEnvKey; key == "" || key == p.ProviderEnvKey {
		c.keep(ctx, p, s)
	}
	return s, nil
}

// keep records s, a successful read of p, as the last good read of p's
// route, with the account key the read recorded, and drops reads too old to
// lend. A read that recorded no account is not kept. It is best effort: a
// lost write leaves an older read, or none, to lend.
func (c *Cache) keep(ctx context.Context, p workerenv.Process, s quota.Snapshot) {
	if c.State == nil {
		return
	}
	now := c.now()
	_ = c.State.Update(ctx, func(st *State) error {
		for key, last := range st.LastReads {
			if now.Sub(last.Snapshot.ReadAt) > lendMaxAge {
				delete(st.LastReads, key)
			}
		}
		account := st.AccountKeys[p.Agent+"/"+quota.IdentityAccount]
		if account == "" {
			return nil
		}
		if st.LastReads == nil {
			st.LastReads = map[string]LastRead{}
		}
		st.LastReads[RouteKey(p)] = LastRead{Account: account, Snapshot: s}
		return nil
	})
}

// lend returns, for a read of p that timed out, the last good read of p's
// route marked as cached, when its account is still the agent's account and
// it is lendable now. ok is false for any other failure, a caller that gave
// up, or no such read.
func (c *Cache) lend(ctx context.Context, p workerenv.Process, readErr error) (quota.Snapshot, bool) {
	if c.State == nil || !errors.Is(readErr, quota.ErrTimedOut) || ctx.Err() != nil {
		return quota.Snapshot{}, false
	}
	var last LastRead
	found := false
	err := c.State.View(ctx, func(st State) error {
		last, found = st.LastReads[RouteKey(p)]
		found = found && last.Account != "" && last.Account == st.AccountKeys[p.Agent+"/"+quota.IdentityAccount]
		return nil
	})
	if err != nil || !found {
		return quota.Snapshot{}, false
	}
	s := last.Snapshot
	s.Cached = true
	if !lendable(s, c.now()) {
		return quota.Snapshot{}, false
	}
	return s, true
}

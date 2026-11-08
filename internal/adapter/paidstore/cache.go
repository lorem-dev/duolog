// Package paidstore keeps what paid runs remember between admissions: the
// route-aware cache of quota reads and the assessments made from them.
package paidstore

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

// Cache keeps the last successful quota read of each launch route, keyed by
// the process's CacheKey, and the assessment made from it. It holds and
// serializes only snapshots, assessments and names: never a process's
// environment, settings or argv. It is safe for concurrent use.
//
// The entries live as long as the process. With Dir set, each read made now
// also goes to a file there, so that the next duolog process can use it in
// place of a read that fails for a passing reason, as Get says.
type Cache struct {
	// Read reads the quota a process sees, quota.Reader.ReadProcess.
	Read func(context.Context, launch.Process) (account.Snapshot, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Dir is the folder of the file of recent good reads, the account state
	// folder; empty keeps no read beyond the process.
	Dir string

	mu      sync.Mutex
	entries map[string]cacheEntry
	envKeys map[string]string
	plans   map[string]string
}

type cacheEntry struct {
	Snapshot   account.Snapshot     `json:"snapshot"`
	Assessment paidrules.Assessment `json:"assessment"`
}

// Get builds the process and assesses it. build runs first on every call, so
// the process, its layers and the endpoint matches are never older than the
// request; the cache is looked up by the rebuilt process's CacheKey, which
// changes with any route fact, never by names alone. A read of the same key
// younger than the quota age limit is reused unless fresh is set; otherwise
// Get reads anew.
//
// When the read names a Codex provider env_key that the process was not
// built with, Get remembers it, builds again and reads again, once, before
// the process is used; build takes the key from ProviderEnvKey. A process
// still built without it is an error.
//
// A failed read drops the key's entry, so no older and cheaper verdict is
// used after it; the assessment is then paid and Get returns the read's
// error with it. The one exception is a transient failure, for a caller that
// did not set fresh and is still waiting, when Dir holds a good read of this
// same key within the quota age limit: that read is used, marked Recalled,
// and judged as it was, so a read that said paid or rejected still does. An
// error from build returns nothing else.
func (c *Cache) Get(ctx context.Context,
	build func() (launch.Process, error), cfg *config.Config,
	evidence account.RunEvidence, fresh bool) (launch.Process, account.Snapshot, paidrules.Assessment, error) {
	if c.Read == nil {
		return launch.Process{}, account.Snapshot{}, paidrules.Assessment{}, errors.New("paid runs: cache has no reader")
	}
	for rebuilt := false; ; rebuilt = true {
		// The plans are read before build, which reads them too: a plan another
		// Get stores after build then still differs from known, and the process
		// is built again with it.
		plans := c.knownPlans()
		p, err := build()
		if err != nil {
			return launch.Process{}, account.Snapshot{}, paidrules.Assessment{}, err
		}
		knownPlan := plans[p.Agent]
		maxAge, readNow := paidrules.QuotaMaxAge(cfg), fresh || rebuilt
		s, cached := c.lookup(p.CacheKey, readNow, maxAge)
		var readErr error
		if !cached {
			s, readErr = c.readOrRecall(ctx, p, readNow, maxAge)
		}
		if key := s.Billing.ProviderEnvKey; key != "" && key != p.ProviderEnvKey {
			c.mu.Lock()
			if c.envKeys == nil {
				c.envKeys = map[string]string{}
			}
			c.envKeys[p.Agent] = key
			c.mu.Unlock()
			if !rebuilt {
				continue
			}
			return p, s, paidrules.Assessment{}, errors.New("paid runs: the process was built without the provider env_key the read found")
		}
		// The plan the read found sets the compact window of a Claude worker or
		// peer. The cache key does not hold it, so the read stays valid: the
		// process is built again once, without a new read.
		if p, err = c.withPlan(p, s, readErr, knownPlan, build); err != nil {
			return launch.Process{}, account.Snapshot{}, paidrules.Assessment{}, err
		}
		a := paidrules.Evaluate(p, s, readErr, evidence, cfg, c.now())
		c.store(p.CacheKey, s, a, readErr)
		if !cached {
			c.remember(p.CacheKey, s, readErr, maxAge)
		}
		return p, s, a, readErr
	}
}

// withPlan keeps the plan a successful Claude read found and, when it differs
// from known, the plan p was built with, builds the process again with it.
// Otherwise it returns p.
func (c *Cache) withPlan(p launch.Process, s account.Snapshot, readErr error, known string,
	build func() (launch.Process, error)) (launch.Process, error) {
	if learned := readErr == nil && p.Agent == "claude" && s.Plan != "" && s.Plan != known; !learned {
		return p, nil
	}
	c.mu.Lock()
	if c.plans == nil {
		c.plans = map[string]string{}
	}
	c.plans[p.Agent] = s.Plan
	c.mu.Unlock()
	return build()
}

// Plan is the plan of agent that an earlier read found, empty when none did.
// build passes it to workerenv.Build as ProcessInput.Plan.
func (c *Cache) Plan(agent string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.plans[agent]
}

// knownPlans copies the plans earlier reads found, by agent.
func (c *Cache) knownPlans() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.plans)
}

// After reads anew the quota that the built process p sees, as Get would,
// and keeps the read for the next Get of p's key. When the read of p's key
// is still younger than the quota age limit, it reads nothing and fresh is
// true. A read that names a provider env_key p was not built with is not
// kept and is an error, as in Get. No earlier read ever stands in for a
// failed one here: the caller measures what changed since the run.
func (c *Cache) After(ctx context.Context, p launch.Process, cfg *config.Config) (s account.Snapshot, fresh bool, err error) {
	if c.Read == nil {
		return account.Snapshot{}, false, errors.New("paid runs: cache has no reader")
	}
	maxAge := paidrules.QuotaMaxAge(cfg)
	if s, ok := c.lookup(p.CacheKey, false, maxAge); ok {
		return s, true, nil
	}
	s, err = c.Read(ctx, p)
	if key := s.Billing.ProviderEnvKey; err == nil && key != "" && key != p.ProviderEnvKey {
		return account.Snapshot{}, false, errors.New("paid runs: the process was built without the provider env_key the read found")
	}
	c.store(p.CacheKey, s, paidrules.Evaluate(p, s, err, account.RunEvidence{}, cfg, c.now()), err)
	c.remember(p.CacheKey, s, err, maxAge)
	return s, false, err
}

// ProviderEnvKey is the env_key of agent's default provider that an earlier
// read found, empty when none did. build passes it to workerenv.Build as
// ProcessInput.ProviderEnvKey.
func (c *Cache) ProviderEnvKey(agent string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.envKeys[agent]
}

// Latest is the most recent successful read of agent from a worker or peer
// process, which a lead report uses as the account's free read: the lead
// itself is never run. ok is false when there is none.
func (c *Cache) Latest(agent string) (account.Snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var best account.Snapshot
	found := false
	for _, entry := range c.entries {
		a := entry.Assessment
		if a.Agent != agent || a.Shape == launch.Lead {
			continue
		}
		if !found || entry.Snapshot.ReadAt.After(best.ReadAt) {
			best, found = entry.Snapshot, true
		}
	}
	return best, found
}

// lookup returns the cached snapshot of key while it is no older than maxAge.
func (c *Cache) lookup(key string, fresh bool, maxAge time.Duration) (account.Snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || fresh {
		return account.Snapshot{}, false
	}
	if !within(entry.Snapshot.ReadAt, c.now(), maxAge) {
		return account.Snapshot{}, false
	}
	return entry.Snapshot, true
}

// store keeps a successful read and its assessment, and forgets the key on a
// failed one.
func (c *Cache) store(key string, s account.Snapshot, a paidrules.Assessment, readErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if readErr != nil {
		delete(c.entries, key)
		return
	}
	if c.entries == nil {
		c.entries = map[string]cacheEntry{}
	}
	c.entries[key] = cacheEntry{s, a}
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// MarshalJSON writes the cached snapshots and assessments in key order, and
// the provider env_key names found. Snapshots and assessments hold names and
// numbers only, and the keys are hashes.
func (c *Cache) MarshalJSON() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	type keyed struct {
		Key string `json:"key"`
		cacheEntry
	}
	out := struct {
		Entries         []keyed           `json:"entries"`
		ProviderEnvKeys map[string]string `json:"provider_env_keys,omitempty"`
	}{Entries: []keyed{}, ProviderEnvKeys: c.envKeys}
	for key, entry := range c.entries {
		out.Entries = append(out.Entries, keyed{key, entry})
	}
	slices.SortFunc(out.Entries, func(a, b keyed) int { return strings.Compare(a.Key, b.Key) })
	return json.Marshal(out)
}

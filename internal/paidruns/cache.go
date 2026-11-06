package paidruns

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Cache keeps the last successful quota read of each launch route, keyed by
// the process's CacheKey, and the assessment made from it. It holds and
// serializes only snapshots, assessments and names: never a process's
// environment, settings or argv. It is safe for concurrent use.
type Cache struct {
	// Read reads the quota a process sees, quota.Reader.ReadProcess.
	Read func(context.Context, workerenv.Process) (quota.Snapshot, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry
	envKeys map[string]string
}

type cacheEntry struct {
	Snapshot   quota.Snapshot `json:"snapshot"`
	Assessment Assessment     `json:"assessment"`
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
// error with it. An error from build returns nothing else.
func (c *Cache) Get(ctx context.Context,
	build func() (workerenv.Process, error), cfg *config.Config,
	evidence quota.RunEvidence, fresh bool) (workerenv.Process, quota.Snapshot, Assessment, error) {
	if c.Read == nil {
		return workerenv.Process{}, quota.Snapshot{}, Assessment{}, errors.New("paid runs: cache has no reader")
	}
	for rebuilt := false; ; rebuilt = true {
		p, err := build()
		if err != nil {
			return workerenv.Process{}, quota.Snapshot{}, Assessment{}, err
		}
		s, cached := c.lookup(p.CacheKey, fresh || rebuilt, quotaMaxAge(cfg))
		var readErr error
		if !cached {
			s, readErr = c.Read(ctx, p)
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
			return p, s, Assessment{}, errors.New("paid runs: the process was built without the provider env_key the read found")
		}
		a := Evaluate(p, s, readErr, evidence, cfg, c.now())
		c.store(p.CacheKey, s, a, readErr)
		return p, s, a, readErr
	}
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
func (c *Cache) Latest(agent string) (quota.Snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var best quota.Snapshot
	found := false
	for _, entry := range c.entries {
		a := entry.Assessment
		if a.Agent != agent || a.Shape == workerenv.Lead {
			continue
		}
		if !found || entry.Snapshot.ReadAt.After(best.ReadAt) {
			best, found = entry.Snapshot, true
		}
	}
	return best, found
}

// lookup returns the cached snapshot of key while it is no older than maxAge.
func (c *Cache) lookup(key string, fresh bool, maxAge time.Duration) (quota.Snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || fresh {
		return quota.Snapshot{}, false
	}
	age := c.now().Sub(entry.Snapshot.ReadAt)
	if entry.Snapshot.ReadAt.IsZero() || age < 0 || age > maxAge {
		return quota.Snapshot{}, false
	}
	return entry.Snapshot, true
}

// store keeps a successful read and its assessment, and forgets the key on a
// failed one.
func (c *Cache) store(key string, s quota.Snapshot, a Assessment, readErr error) {
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

package paidruns

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const (
	trustedURL = "https://llm.example.com/claude"
	movedURL   = "https://moved.example.com/claude"
	cacheKey   = "sk-ant-cache-secret"
)

type cacheInfo struct{ fs.FileInfo }

// ModTime never changes: an edit that keeps the time must still count.
func (cacheInfo) ModTime() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) }

// cacheWorld is one launch whose route inputs a test changes between
// requests: the environment, a settings file, the config, the shape, the
// variant and Codex's provider env_key.
type cacheWorld struct {
	agent    string
	shape    workerenv.Shape
	variant  workerenv.Variant
	cfg      *config.Config
	environ  []string
	files    map[string]string
	envKey   string
	cache    *Cache
	now      time.Time
	builds   int
	reads    int
	readErr  error
	discover string
}

func newCacheWorld(agent string) *cacheWorld {
	cfg := &config.Config{EnvPassthrough: []string{"HOME", "ANTHROPIC_BASE_URL", "CORP_LLM_KEY"}}
	cfg.PaidRuns.Claude.Allow, cfg.PaidRuns.Claude.DailyCapUSD = true, 5
	cfg.PaidRuns.Claude.SubscriptionEndpoints = map[string]string{"corp": trustedURL}
	w := &cacheWorld{
		agent: agent, shape: workerenv.Worker, variant: workerenv.Paid, cfg: cfg,
		environ: []string{"HOME=/home/test", "ANTHROPIC_BASE_URL=" + trustedURL, "CORP_LLM_KEY=" + cacheKey},
		files:   map[string]string{"settings.json": `{"env": {"ANTHROPIC_BASE_URL": "` + trustedURL + `"}}`},
		now:     time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
	}
	if agent == "codex" {
		w.variant = workerenv.Free
		w.files = map[string]string{"settings.json": `model = "gpt"`}
	}
	w.cache = &Cache{Read: w.read, Now: func() time.Time { return w.now }}
	return w
}

// build reads the layer and builds the process afresh, as a launch path
// does, with the provider env_key the cache learned from earlier reads.
func (w *cacheWorld) build() (workerenv.Process, error) {
	w.builds++
	read := func(path string) ([]byte, error) {
		data, ok := w.files[path]
		switch {
		case !ok:
			return nil, fs.ErrNotExist
		case data == "denied":
			return nil, fs.ErrPermission
		}
		return []byte(data), nil
	}
	stat := func(string) (os.FileInfo, error) { return cacheInfo{}, nil }
	layer := workerenv.Layer{Name: "user", Path: "settings.json"}
	facts := workerenv.ReadLayer(w.agent, layer, w.cfg.PaidRuns.Claude.SubscriptionEndpoints, read, stat)
	envKey := w.envKey
	if envKey == "" {
		envKey = w.cache.ProviderEnvKey(w.agent)
	}
	return workerenv.Build(workerenv.ProcessInput{
		Agent: w.agent, Shape: w.shape, Variant: w.variant, Config: w.cfg, Environ: w.environ,
		Layers: []workerenv.LayerFacts{facts}, ProviderEnvKey: envKey, LeadID: "lead-1",
	})
}

// read is a subscription reply, or the configured failure.
func (w *cacheWorld) read(_ context.Context, p workerenv.Process) (quota.Snapshot, error) {
	w.reads++
	if p.Agent != w.agent {
		return quota.Snapshot{}, errors.New("read another agent's process")
	}
	window := quota.Window{Kind: "session", UsedPercent: 10, ResetsAt: w.now.Add(time.Hour)}
	s := quota.Snapshot{Windows: []quota.Window{window}, ReadAt: w.now}
	switch w.agent {
	case "claude":
		s.Billing = quota.BillingFacts{AuthMethod: "claude.ai", RouteKnown: true}
	case "codex":
		allowed := true
		s.Billing = quota.BillingFacts{AccountType: "chatgpt", RouteKnown: true, OrdinaryUsageAllowed: &allowed,
			ProviderEnvKey: w.discover}
		if w.discover != "" {
			s.Billing.RouteSignals = []workerenv.Signal{{Name: "provider:env_key", Layer: "config/read"}}
		}
	}
	if w.readErr != nil {
		return quota.Snapshot{Billing: s.Billing}, w.readErr
	}
	return s, nil
}

func (w *cacheWorld) get(t *testing.T, fresh bool) (workerenv.Process, Assessment, error) {
	t.Helper()
	builds := w.builds
	p, _, a, err := w.cache.Get(context.Background(), w.build, w.cfg, quota.RunEvidence{}, fresh)
	if w.builds == builds {
		t.Fatal("Get reused a process without building it")
	}
	return p, a, err
}

// mustGet is get for a request that must succeed; only the reads it counts matter.
func (w *cacheWorld) mustGet(t *testing.T, fresh bool) {
	t.Helper()
	if _, _, err := w.get(t, fresh); err != nil {
		t.Fatal(err)
	}
}

// Every request rebuilds the process and reads its layers before the cache
// is consulted, so a URL that changes under the same name, a settings edit
// that keeps the modification time, a layer that disappears or cannot be
// read, and a change of config, shape, variant or provider env_key each miss
// the cache. A failure never falls back to an older, cheaper verdict.
func TestCacheRebuildsSameNameEndpointBeforeReuse(t *testing.T) {
	type step struct {
		name    string
		change  func(*cacheWorld)
		reread  bool
		verdict Verdict
	}
	for _, c := range []step{
		{"env URL under the same name", func(w *cacheWorld) {
			w.environ[1] = "ANTHROPIC_BASE_URL=" + movedURL
		}, true, Paid},
		{"settings URL with the same mtime", func(w *cacheWorld) {
			w.files["settings.json"] = `{"env": {"ANTHROPIC_BASE_URL": "` + movedURL + `"}}`
		}, true, Paid},
		{"layer disappears", func(w *cacheWorld) { delete(w.files, "settings.json") }, true, Subscription},
		{"layer unreadable", func(w *cacheWorld) { w.files["settings.json"] = "denied" }, true, Paid},
		{"config endpoint moves", func(w *cacheWorld) {
			w.cfg.PaidRuns.Claude.SubscriptionEndpoints = map[string]string{"corp": movedURL}
		}, true, Paid},
		{"config cap", func(w *cacheWorld) { w.cfg.PaidRuns.Claude.DailyCapUSD = 6 }, true, Subscription},
		{"shape", func(w *cacheWorld) { w.shape = workerenv.Peer }, true, Subscription},
		{"variant", func(w *cacheWorld) { w.variant = workerenv.Free }, true, Subscription},
		{"nothing", func(*cacheWorld) {}, false, Subscription},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newCacheWorld("claude")
			p, a, err := w.get(t, false)
			if err != nil || a.Verdict != Subscription || !slices.Equal(p.EndpointEntries, []string{"corp"}) || w.reads != 1 {
				t.Fatalf("first: %+v %v reads=%d", a, err, w.reads)
			}
			w.now = w.now.Add(30 * time.Second)
			c.change(w)
			_, a, err = w.get(t, false)
			if err != nil || a.Verdict != c.verdict {
				t.Fatalf("after change: %s %v %+v", a.Verdict, err, a.Signals)
			}
			if reread := w.reads == 2; reread != c.reread {
				t.Fatalf("reads = %d, want a re-read %v", w.reads, c.reread)
			}
			if c.verdict == Paid && len(a.Signals) == 0 {
				t.Fatal("paid without a signal")
			}
		})
	}

	// Codex: a changed provider env_key is a new route.
	w := newCacheWorld("codex")
	if _, a, err := w.get(t, false); err != nil || a.Verdict != Subscription || w.reads != 1 {
		t.Fatalf("codex first: %+v %v", a, err)
	}
	w.envKey = "CORP_LLM_KEY"
	p, _, _ := w.get(t, false)
	if w.reads != 2 || !slices.Contains(p.Dropped, "CORP_LLM_KEY") {
		t.Fatalf("env_key change: reads=%d dropped=%v", w.reads, p.Dropped)
	}

	// A provider env_key that the read discovers forces one rebuild and one
	// re-read before the process is used, and then the cache holds.
	w = newCacheWorld("codex")
	w.discover = "CORP_LLM_KEY"
	p, a, err := w.get(t, false)
	if err != nil || w.builds != 2 || w.reads != 2 || p.ProviderEnvKey != "CORP_LLM_KEY" || !slices.Contains(p.Dropped, "CORP_LLM_KEY") {
		t.Fatalf("discovered env_key: builds=%d reads=%d %+v %v", w.builds, w.reads, p, err)
	}
	if a.Verdict != Paid || !slices.Contains(a.Signals, workerenv.Signal{Name: "provider:env_key", Layer: "config/read"}) {
		t.Fatalf("discovered env_key verdict: %+v", a)
	}
	for _, pair := range p.Env {
		if strings.Contains(pair, cacheKey) {
			t.Fatal("the rebuilt free process kept the provider key")
		}
	}
	if p, _, _ = w.get(t, false); w.builds != 3 || w.reads != 2 || p.ProviderEnvKey != "CORP_LLM_KEY" {
		t.Fatalf("after discovery: builds=%d reads=%d", w.builds, w.reads)
	}

	// The cache holds a read for 120 s. fresh forces a read; a failed read is
	// paid and leaves nothing to reuse, so the next request reads again.
	w = newCacheWorld("claude")
	w.mustGet(t, false)
	w.now = w.now.Add(120 * time.Second)
	if w.mustGet(t, false); w.reads != 1 {
		t.Fatalf("reads within 120 s = %d", w.reads)
	}
	w.now = w.now.Add(time.Second)
	if w.mustGet(t, false); w.reads != 2 {
		t.Fatalf("reads after 121 s = %d", w.reads)
	}
	if w.mustGet(t, true); w.reads != 3 {
		t.Fatalf("fresh reads = %d", w.reads)
	}
	w.readErr = errors.New("read timed out or canceled")
	if _, a, err := w.get(t, true); err == nil || a.Verdict != Paid || w.reads != 4 {
		t.Fatalf("failed read: %+v %v reads=%d", a, err, w.reads)
	}
	w.readErr = nil
	w.now = w.now.Add(time.Second)
	if _, a, err := w.get(t, false); err != nil || a.Verdict != Subscription || w.reads != 5 {
		t.Fatalf("after a failure: %+v %v reads=%d", a, err, w.reads)
	}

	// A build that fails is an error, and nothing is read.
	w.agent = "other"
	if _, _, _, err := w.cache.Get(context.Background(), w.build, w.cfg, quota.RunEvidence{}, false); err == nil || w.reads != 5 {
		t.Fatalf("failed build: %v reads=%d", err, w.reads)
	}

	// The cache serializes snapshots and assessments by name only.
	w = newCacheWorld("claude")
	w.mustGet(t, false)
	w.environ[1] = "ANTHROPIC_BASE_URL=" + movedURL
	w.mustGet(t, false)
	data, err := json.Marshal(w.cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"example.com", cacheKey, "/home/test", "settings.json"} {
		if strings.Contains(string(data), value) {
			t.Errorf("cache JSON has %q: %s", value, data)
		}
	}
	if !strings.Contains(string(data), "env:ANTHROPIC_BASE_URL") || !strings.Contains(string(data), `"subscription"`) {
		t.Errorf("cache JSON lacks its assessments: %s", data)
	}
}

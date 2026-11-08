package paidstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

const passing = account.TransientError("usage read failed")

// recentWorld is a cache world whose good reads outlive the process.
func recentWorld(t *testing.T) *cacheWorld {
	t.Helper()
	w := newCacheWorld("claude")
	w.cache.Dir = t.TempDir()
	return w
}

// restart is the next duolog command: an empty cache on the same account
// state folder.
func (w *cacheWorld) restart() {
	w.cache = &Cache{Read: w.read, Now: w.cache.Now, Dir: w.cache.Dir}
}

func (w *cacheWorld) getFull(ctx context.Context, fresh bool) (account.Snapshot, paidrules.Assessment, error) {
	_, s, a, err := w.cache.Get(ctx, w.build, w.cfg, account.RunEvidence{}, fresh)
	return s, a, err
}

func hasSignal(a paidrules.Assessment, name string) bool {
	return slices.ContainsFunc(a.Signals, func(s launch.Signal) bool { return s.Name == name })
}

// A passing failure of the next command uses the last good read of the route
// while it is within the quota age limit, and says so.
func TestPassingFailureUsesARecentGoodRead(t *testing.T) {
	w := recentWorld(t)
	first := w.now
	w.mustGet(t, false)
	w.restart()
	w.now = first.Add(30 * time.Second)
	w.readErr = passing
	s, a, err := w.getFull(context.Background(), false)
	if err != nil || a.Verdict != paidrules.Subscription {
		t.Fatalf("%+v %v", a, err)
	}
	if !s.Recalled || !s.ReadAt.Equal(first) || len(s.Windows) != 1 {
		t.Fatalf("%+v", s)
	}
	if w.reads != 2 {
		t.Fatalf("reads = %d; the failed read must still be tried", w.reads)
	}
	if got, ok := w.cache.Latest("claude"); !ok || !got.Recalled {
		t.Fatalf("latest: %+v %v", got, ok)
	}

	// Using the read does not make it younger: the limit counts from the read.
	w.restart()
	w.now = first.Add(120 * time.Second)
	if _, a, err = w.getFull(context.Background(), false); err != nil || a.Verdict != paidrules.Subscription {
		t.Fatalf("at the limit: %+v %v", a, err)
	}
	w.restart()
	w.now = first.Add(121 * time.Second)
	if _, a, err = w.getFull(context.Background(), false); !errors.Is(err, passing) || !hasSignal(a, "read:failed") {
		t.Fatalf("past the limit: %+v %v", a, err)
	}
}

// Every other way to arrive at a failed read stays paid.
func TestFailedReadStaysPaidWithoutARecentGoodRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		noRead bool
		fresh  bool
		before func(*cacheWorld, *testing.T)
		ctx    func() context.Context
	}{
		{name: "no good read", noRead: true},
		{name: "asked for a read made now", fresh: true},
		{name: "older than the limit", before: func(w *cacheWorld, _ *testing.T) { w.now = w.now.Add(121 * time.Second) }},
		{name: "read from the future", before: func(w *cacheWorld, _ *testing.T) { w.now = w.now.Add(-time.Minute) }},
		{name: "route changed", before: func(w *cacheWorld, _ *testing.T) { w.environ[1] = "ANTHROPIC_BASE_URL=" + movedURL }},
		{name: "settings changed", before: func(w *cacheWorld, _ *testing.T) {
			w.files["settings.json"] = `{"env": {"ANTHROPIC_BASE_URL": "` + movedURL + `"}}`
		}},
		{name: "a definite failure came between", before: func(w *cacheWorld, t *testing.T) {
			w.readErr = errors.New("not logged in")
			if _, a, err := w.getFull(context.Background(), false); err == nil || a.Verdict != paidrules.Paid {
				t.Fatalf("%+v %v", a, err)
			}
			w.restart()
		}},
		{name: "the caller gave up", ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := recentWorld(t)
			if !tc.noRead {
				w.mustGet(t, false)
				w.restart()
			}
			if tc.before != nil {
				tc.before(w, t)
			}
			ctx := context.Background()
			if tc.ctx != nil {
				ctx = tc.ctx()
			}
			w.readErr = passing
			s, a, err := w.getFull(ctx, tc.fresh)
			if !errors.Is(err, passing) || a.Verdict != paidrules.Paid || !hasSignal(a, "read:failed") || s.Recalled {
				t.Fatalf("%+v %+v %v", s, a, err)
			}
		})
	}
}

// A definite failure is not covered, even with a good read a second old.
func TestDefiniteFailureIsNotCovered(t *testing.T) {
	w := recentWorld(t)
	w.mustGet(t, false)
	w.restart()
	w.readErr = errors.New("no quota data; check login")
	if _, a, err := w.getFull(context.Background(), false); err == nil || a.Verdict != paidrules.Paid {
		t.Fatalf("%+v %v", a, err)
	}
}

// A used read keeps what it said: paid stays paid and rejected stays rejected.
func TestRecalledReadKeepsItsVerdict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*account.Snapshot)
		check  func(account.Snapshot, paidrules.Assessment) bool
	}{
		{"paid route", func(s *account.Snapshot) {
			s.Billing.AuthMethod = "api_key"
			s.Billing.RouteSignals = []launch.Signal{{Name: "auth:api_key", Layer: "auth status"}}
		}, func(_ account.Snapshot, a paidrules.Assessment) bool {
			return a.Verdict == paidrules.Paid && hasSignal(a, "auth:api_key")
		}},
		{"windows refused", func(s *account.Snapshot) {
			no := false
			s.Billing.WindowsAvailable = &no
		}, func(_ account.Snapshot, a paidrules.Assessment) bool {
			return a.Verdict == paidrules.Paid && hasSignal(a, "read:windows-unavailable")
		}},
		{"rejected", func(s *account.Snapshot) { s.Rejected = true }, func(s account.Snapshot, _ paidrules.Assessment) bool {
			return paidrules.FamilyBlocked(s, "")
		}},
		{"overage on", func(s *account.Snapshot) { s.Billing.OverageEnabled = true }, func(_ account.Snapshot, a paidrules.Assessment) bool {
			return a.Verdict == paidrules.SubscriptionOverage
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := recentWorld(t)
			w.cache.Read = func(ctx context.Context, p launch.Process) (account.Snapshot, error) {
				s, err := w.read(ctx, p)
				tc.change(&s)
				return s, err
			}
			if _, _, err := w.getFull(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			w.restart()
			w.readErr = passing
			s, a, err := w.getFull(context.Background(), false)
			if err != nil || !s.Recalled || !tc.check(s, a) {
				t.Fatalf("%+v %+v %v", s, a, err)
			}
		})
	}
}

// A newer read that says paid replaces the good read before it.
func TestNewerPaidReadReplacesTheGoodRead(t *testing.T) {
	w := recentWorld(t)
	w.mustGet(t, false)
	w.restart()
	w.now = w.now.Add(10 * time.Second)
	w.cache.Read = func(ctx context.Context, p launch.Process) (account.Snapshot, error) {
		s, err := w.read(ctx, p)
		s.Billing.AuthMethod = "api_key"
		s.Billing.RouteSignals = []launch.Signal{{Name: "auth:api_key", Layer: "auth status"}}
		return s, err
	}
	if _, a, err := w.getFull(context.Background(), false); err != nil || a.Verdict != paidrules.Paid {
		t.Fatalf("%+v %v", a, err)
	}
	w.restart()
	w.readErr = passing
	if _, a, err := w.getFull(context.Background(), false); err != nil || a.Verdict != paidrules.Paid || !hasSignal(a, "auth:api_key") {
		t.Fatalf("%+v %v", a, err)
	}
}

// A passing failure of the read after a run keeps the good read too, but is
// never replaced by it: learning needs a read made now.
func TestAfterKeepsTheGoodReadOverAPassingFailure(t *testing.T) {
	w := recentWorld(t)
	p, _, err := w.get(t, false)
	if err != nil {
		t.Fatal(err)
	}
	w.restart()
	w.now = w.now.Add(20 * time.Second)
	w.readErr = passing
	if s, fresh, err := w.cache.After(context.Background(), p, w.cfg); !errors.Is(err, passing) || fresh || s.Recalled {
		t.Fatalf("%+v %v %v", s, fresh, err)
	}
	w.restart()
	if s, a, err := w.getFull(context.Background(), false); err != nil || !s.Recalled || a.Verdict != paidrules.Subscription {
		t.Fatalf("%+v %+v %v", s, a, err)
	}
}

// A broken or foreign file is no read: the failure stays paid, and the next
// good read replaces the file.
func TestBrokenReadsFileIsNoRead(t *testing.T) {
	for name, content := range map[string]string{
		"not json":      "{",
		"other version": `{"version":99,"reads":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := recentWorld(t)
			path := filepath.Join(w.cache.Dir, readsFile)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			w.readErr = passing
			if _, a, err := w.getFull(context.Background(), false); !errors.Is(err, passing) || a.Verdict != paidrules.Paid {
				t.Fatalf("%+v %v", a, err)
			}
			w.readErr = nil
			w.mustGet(t, false)
			w.restart()
			w.readErr = passing
			if s, _, err := w.getFull(context.Background(), false); err != nil || !s.Recalled {
				t.Fatalf("%+v %v", s, err)
			}
		})
	}
}

// A folder that cannot hold the file costs the fallback and nothing else.
func TestUnwritableFolderIsNoFailure(t *testing.T) {
	w := recentWorld(t)
	w.cache.Dir = filepath.Join(w.cache.Dir, "missing", "folder")
	w.mustGet(t, false)
	w.restart()
	w.readErr = passing
	if _, a, err := w.getFull(context.Background(), false); !errors.Is(err, passing) || a.Verdict != paidrules.Paid {
		t.Fatalf("%+v %v", a, err)
	}
}

// The file holds what the cache may serialize, privately, and only reads
// that are still within the limit.
func TestReadsFileIsPrivateAndPruned(t *testing.T) {
	w := recentWorld(t)
	w.mustGet(t, false)
	path := filepath.Join(w.cache.Dir, readsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{cacheKey, trustedURL} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("file holds %q: %s", secret, data)
		}
	}
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", info.Mode(), err)
	}

	// Another route, past the limit: the first route's read is dropped.
	w.restart()
	w.now = w.now.Add(121 * time.Second)
	w.environ[1] = "ANTHROPIC_BASE_URL=" + movedURL
	w.mustGet(t, false)
	if data, err = os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
	var file struct {
		Reads map[string]json.RawMessage `json:"reads"`
	}
	if err := json.Unmarshal(data, &file); err != nil || len(file.Reads) != 1 {
		t.Fatalf("%v: %s", err, data)
	}
}

// A cache without a folder keeps no read: tests and one-shot gates do not
// share anything, and a failure stays paid.
func TestCacheWithoutFolderKeepsNoRead(t *testing.T) {
	w := newCacheWorld("claude")
	w.mustGet(t, false)
	w.restart()
	w.readErr = passing
	if _, a, err := w.getFull(context.Background(), false); !errors.Is(err, passing) || a.Verdict != paidrules.Paid {
		t.Fatalf("%+v %v", a, err)
	}
}

package telemetry

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestHealthSinceProcessStart(t *testing.T) {
	noOverride(t)
	base := tempBase(t)
	s := mustOpenStore(t, storeOptions(base))
	h := s.Health()
	if h.Since.IsZero() || h.Since.After(time.Now()) {
		t.Fatalf("Since %v, want the process start", h.Since)
	}

	// The counters belong to the process, not to one Store, and a new Store
	// does not restart them.
	bad := sampleRun()
	bad.Attempt = 0
	s.Append(bad)
	again := mustOpenStore(t, storeOptions(base))
	got := again.Health()
	if got.Since != h.Since {
		t.Errorf("Since moved from %v to %v", h.Since, got.Since)
	}
	if d, f := healthDelta(h, got); d != 1 || f != 1 {
		t.Errorf("a new Store sees %d dropped, %d failures; want 1 and 1", d, f)
	}
	var none *Store
	if none.Health() != got {
		t.Error("Health of no Store differs from the process counters")
	}

	// Failures leave no counter file: the root holds the salt, the backup
	// tag, the prune stamp and segments with their locks only.
	s.Append(sampleRun())
	root := storeRoot(t, base)
	allowed := regexp.MustCompile(`^(?:salt|CACHEDIR\.TAG|prune\.stamp|v1|v1/(?:_account|[0-9a-f]{16})(?:/(?:telemetry\.lock|[0-9]{4}-[0-9]{2}-[0-9]{2}\.ndjson))?)$`)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if !allowed.MatchString(filepath.ToSlash(rel)) {
			t.Errorf("unexpected entry %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "v1")); err != nil {
		t.Fatal(err)
	}
}

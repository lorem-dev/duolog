package telemetry

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a store clock a test can move.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// pruneFixture opens a store with collection on under a fresh base, on a
// clock the test can move, and returns it with its root.
func pruneFixture(t *testing.T, at time.Time, retention int) (*Store, string, *clock) {
	t.Helper()
	noOverride(t)
	base := tempBase(t)
	c := &clock{now: at}
	o := storeOptions(base)
	o.RetentionDays = retention
	o.Now = c.Now
	s := mustOpenStore(t, o)
	return s, storeRoot(t, base), c
}

func mustPrune(t *testing.T, s *Store, force bool) Maintenance {
	t.Helper()
	m, err := s.Prune(force)
	if err != nil {
		t.Fatalf("Prune(%v): %v", force, err)
	}
	return m
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func stampTime(t *testing.T, root string) time.Time {
	t.Helper()
	info, err := os.Lstat(filepath.Join(root, "prune.stamp"))
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

func setStamp(t *testing.T, root string, at time.Time) {
	t.Helper()
	path := filepath.Join(root, "prune.stamp")
	if !exists(t, path) {
		plant(t, path, nil)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// countPasses counts the maintenance passes that start.
func countPasses(t *testing.T) *atomic.Int64 {
	t.Helper()
	n := &atomic.Int64{}
	setRaceHook(t, func(stage string) {
		if stage == "prune" {
			n.Add(1)
		}
	})
	return n
}

func dayStart(day string) time.Time {
	d, err := time.Parse(time.DateOnly, day)
	if err != nil {
		panic(err)
	}
	return d
}

func TestPruneBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		retention int
		now       time.Time
		day       string
		deleted   bool
	}{
		{"at D+30 days", 30, dayStart("2026-10-06").Add(30 * day), "2026-10-06", true},
		{"just before D+30 days", 30, dayStart("2026-10-06").Add(30*day - time.Second), "2026-10-06", false},
		{"shorter user retention", 7, dayStart("2026-10-29").Add(7 * day), "2026-10-29", true},
		{"just before user retention", 7, dayStart("2026-10-29").Add(7*day - time.Second), "2026-10-29", false},
		{"retention over 30 days stays 30", 90, dayStart("2026-10-06").Add(30 * day), "2026-10-06", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, root, _ := pruneFixture(t, c.now, c.retention)
			at := dayStart(c.day).Add(12 * time.Hour)
			run, err := Encode(runAttempt(1), testNames, at)
			if err != nil {
				t.Fatal(err)
			}
			project := projectSegment(t, root, c.day)
			lines := map[string][]byte{accountSegment(root, c.day): quotaLine(t, 1, at), project: run}
			var planted int64
			for seg, line := range lines {
				// A crash after the rename of a compression leaves both.
				plant(t, seg, line)
				plant(t, seg+".gz", gzipped(t, line))
				planted += int64(len(line) + len(gzipped(t, line)))
			}
			m := mustPrune(t, s, true)
			for _, seg := range []string{accountSegment(root, c.day), project} {
				plain, gz := exists(t, seg), exists(t, seg+".gz")
				if c.deleted && (plain || gz) {
					t.Errorf("%s kept at %v: plain %v, gz %v", filepath.Base(seg), c.now, plain, gz)
				}
				if !c.deleted && (plain || !gz) {
					t.Errorf("%s at %v: plain %v, gz %v, want the compressed copy only", filepath.Base(seg), c.now, plain, gz)
				}
			}
			if c.deleted {
				if m.Deleted != 4 || m.Compressed != 0 || m.Skipped != 0 || m.Bytes != planted {
					t.Errorf("maintenance %+v, want 4 deleted, %d bytes", m, planted)
				}
				if got := mustRead(t, s, Query{}); len(got) != 0 {
					t.Errorf("read %d events after deletion", len(got))
				}
				return
			}
			if m.Deleted != 0 || m.Compressed != 2 || m.Skipped != 0 {
				t.Errorf("maintenance %+v, want 2 compressed", m)
			}
			if got, want := epochs(t, mustRead(t, s, Query{})), []int64{1, -1}; !slices.Equal(got, want) {
				t.Errorf("read %v, want %v", got, want)
			}
		})
	}

	t.Run("backward clock delays and persists no clock", func(t *testing.T) {
		s, root, c := pruneFixture(t, testNow.Add(-10*day), 30)
		// Expired at testNow, not yet at the clock set back.
		expired := accountSegment(root, "2026-10-01")
		plant(t, expired, quotaLine(t, 1, dayStart("2026-10-01").Add(time.Hour)))
		// Named for a day after the clock set back: written before it.
		ahead := accountSegment(root, "2026-11-04")
		plant(t, ahead, quotaLine(t, 2, dayStart("2026-11-04").Add(time.Hour)))

		m := mustPrune(t, s, false)
		if exists(t, expired) || !exists(t, expired+".gz") {
			t.Errorf("segment pruned or left plain under a clock set back: %+v", m)
		}
		if !exists(t, ahead) || exists(t, ahead+".gz") {
			t.Error("a segment named after today was compressed")
		}
		if got := stampTime(t, root); !got.Equal(c.Now()) {
			t.Errorf("stamp %v, want the clock %v", got, c.Now())
		}
		// Nothing but the store's own files: no clock state, no temporary.
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || path == root || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if m := fileName.FindStringSubmatch(rel); m == nil || filepath.Ext(rel) == ".tmp" {
				t.Errorf("unexpected file %s", rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		// The clock comes back: the stamp is old and the segment expired.
		c.set(testNow)
		if m := mustPrune(t, s, false); m.Deleted != 1 || exists(t, expired+".gz") {
			t.Errorf("after the clock came back: %+v", m)
		}
		// A stamp ahead of the clock never holds pruning back.
		c.set(testNow.Add(-10 * day))
		passes := countPasses(t)
		mustPrune(t, s, false)
		if passes.Load() != 1 {
			t.Errorf("%d passes with the stamp ahead of the clock, want 1", passes.Load())
		}
	})
}

func TestPruneCadence(t *testing.T) {
	expiredDay := "2026-10-01"

	t.Run("missing stamp", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		plant(t, accountSegment(root, expiredDay), quotaLine(t, 1, testNow))
		if exists(t, filepath.Join(root, "prune.stamp")) {
			t.Fatal("stamp before the first prune")
		}
		if m := mustPrune(t, s, false); m.Deleted != 1 {
			t.Errorf("maintenance %+v, want 1 deleted", m)
		}
		if got := stampTime(t, root); !got.Equal(testNow) {
			t.Errorf("stamp %v, want %v", got, testNow)
		}
	})

	t.Run("less than six hours", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		plant(t, accountSegment(root, expiredDay), quotaLine(t, 1, testNow))
		last := testNow.Add(-6*time.Hour + time.Second)
		setStamp(t, root, last)
		passes := countPasses(t)
		if m := mustPrune(t, s, false); m != (Maintenance{}) {
			t.Errorf("maintenance %+v, want none", m)
		}
		if passes.Load() != 0 || !exists(t, accountSegment(root, expiredDay)) {
			t.Error("pruned before the interval")
		}
		if got := stampTime(t, root); !got.Equal(last) {
			t.Errorf("stamp moved to %v", got)
		}
	})

	t.Run("older than six hours", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		plant(t, accountSegment(root, expiredDay), quotaLine(t, 1, testNow))
		setStamp(t, root, testNow.Add(-6*time.Hour))
		if m := mustPrune(t, s, false); m.Deleted != 1 {
			t.Errorf("maintenance %+v, want 1 deleted", m)
		}
		if got := stampTime(t, root); !got.Equal(testNow) {
			t.Errorf("stamp %v, want %v", got, testNow)
		}
	})

	t.Run("force", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		plant(t, accountSegment(root, expiredDay), quotaLine(t, 1, testNow))
		setStamp(t, root, testNow)
		if m := mustPrune(t, s, true); m.Deleted != 1 {
			t.Errorf("maintenance %+v, want 1 deleted", m)
		}
	})

	t.Run("concurrent due checks", func(t *testing.T) {
		noOverride(t)
		base := tempBase(t)
		root := storeRoot(t, base)
		var stores []*Store
		for range 8 {
			stores = append(stores, mustOpenStore(t, storeOptions(base)))
		}
		plant(t, accountSegment(root, expiredDay), quotaLine(t, 1, testNow))
		passes := countPasses(t)
		var deleted atomic.Int64
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, s := range stores {
			wg.Go(func() {
				<-start
				m, err := s.Prune(false)
				if err != nil {
					t.Error(err)
				}
				deleted.Add(int64(m.Deleted))
			})
		}
		close(start)
		wg.Wait()
		if passes.Load() != 1 || deleted.Load() != 1 {
			t.Errorf("%d passes deleted %d files, want one pass", passes.Load(), deleted.Load())
		}
	})

	t.Run("disabled collection", func(t *testing.T) {
		noOverride(t)
		base := tempBase(t)
		mustOpenStore(t, storeOptions(base)).Close()
		root := storeRoot(t, base)
		plant(t, accountSegment(root, expiredDay), quotaLine(t, 1, testNow))
		o := storeOptions(base)
		o.Enabled = false
		if m := mustPrune(t, mustOpenStore(t, o), false); m.Deleted != 1 {
			t.Errorf("collection off: %+v, want 1 deleted", m)
		}

		plant(t, accountSegment(root, expiredDay), quotaLine(t, 1, testNow))
		t.Setenv(envSwitch, "off")
		if m := mustPrune(t, mustOpenStore(t, storeOptions(base)), true); m.Deleted != 1 {
			t.Errorf("%s=off: %+v, want 1 deleted", envSwitch, m)
		}

		// With collection off and no root, Prune creates nothing.
		fresh := tempBase(t)
		o.Location = storeOptions(fresh).Location
		if m := mustPrune(t, mustOpenStore(t, o), true); m != (Maintenance{}) {
			t.Errorf("no root: %+v", m)
		}
		if exists(t, storeRoot(t, fresh)) {
			t.Error("prune created the root")
		}
	})
}

func TestPruneDoesNotFollowLinks(t *testing.T) {
	const expiredDay, closedDay = "2026-10-01", "2026-11-04"
	content := func(t *testing.T) []byte { return quotaLine(t, 9, testNow) }
	same := func(t *testing.T, path string, want []byte) {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s changed: %q, %v", filepath.Base(path), got, err)
		}
	}

	t.Run("symlinked partition", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		outside := filepath.Join(filepath.Dir(root), "outside")
		data := content(t)
		for _, d := range []string{expiredDay, closedDay} {
			plant(t, filepath.Join(outside, d+".ndjson"), data)
		}
		if err := os.MkdirAll(filepath.Join(root, "v1"), 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "v1", otherID)
		plantReparse(t, outside, link)
		mustPrune(t, s, true)
		if !exists(t, link) {
			t.Error("the partition link was removed")
		}
		for _, d := range []string{expiredDay, closedDay} {
			same(t, filepath.Join(outside, d+".ndjson"), data)
		}
		if exists(t, filepath.Join(outside, closedDay+".ndjson.gz")) {
			t.Error("compressed through a partition link")
		}
	})

	t.Run("symlinked segments", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		outside := filepath.Join(filepath.Dir(root), "outside.ndjson")
		data := content(t)
		plant(t, outside, data)
		if err := os.MkdirAll(filepath.Join(root, "v1", "_account"), 0o700); err != nil {
			t.Fatal(err)
		}
		links := []string{accountSegment(root, expiredDay), accountSegment(root, closedDay),
			accountSegment(root, expiredDay) + ".gz", accountSegment(root, "2026-11-03") + ".gz.tmp"}
		for _, l := range links {
			symlinkOrSkip(t, outside, l)
		}
		m := mustPrune(t, s, true)
		for _, l := range links {
			info, err := os.Lstat(l)
			if err != nil || info.Mode()&fs.ModeSymlink == 0 {
				t.Errorf("%s: link gone or replaced: %v", filepath.Base(l), err)
			}
		}
		same(t, outside, data)
		if m.Deleted != 0 || m.Compressed != 0 || m.Skipped != len(links) {
			t.Errorf("maintenance %+v, want %d skipped", m, len(links))
		}
	})

	t.Run("hard links", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		dir := filepath.Dir(root)
		data := content(t)
		expiredOut, closedOut := filepath.Join(dir, "expired.out"), filepath.Join(dir, "closed.out")
		plant(t, expiredOut, data)
		plant(t, closedOut, data)
		if err := os.MkdirAll(filepath.Join(root, "v1", "_account"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(expiredOut, accountSegment(root, expiredDay)); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(closedOut, accountSegment(root, closedDay)); err != nil {
			t.Fatal(err)
		}
		m := mustPrune(t, s, true)
		// Removing an expired name unlinks only that name.
		if exists(t, accountSegment(root, expiredDay)) {
			t.Error("expired hard link kept")
		}
		// A file with another name is never read into a compressed copy.
		if !exists(t, accountSegment(root, closedDay)) || exists(t, accountSegment(root, closedDay)+".gz") {
			t.Error("hard-linked segment compressed")
		}
		same(t, expiredOut, data)
		same(t, closedOut, data)
		if m.Deleted != 1 || m.Compressed != 0 || m.Skipped != 1 {
			t.Errorf("maintenance %+v, want 1 deleted, 1 skipped", m)
		}
	})

	t.Run("malformed and unrelated entries", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		data := content(t)
		account := filepath.Join(root, "v1", "_account")
		kept := []string{
			filepath.Join(account, "2026-02-30.ndjson"),
			filepath.Join(account, "2026-1-01.ndjson"),
			filepath.Join(account, "20261001.ndjson"),
			filepath.Join(account, "2026-02-30.ndjson.gz.tmp"),
			filepath.Join(account, expiredDay+".ndjson.bak"),
			filepath.Join(account, expiredDay+".json"),
			filepath.Join(account, expiredDay+".ndjson.gz.tmp.old"),
			filepath.Join(account, "notes.txt"),
			filepath.Join(root, "v1", "notes"),
			filepath.Join(root, "v1", "ABCDEF0123456789", expiredDay+".ndjson"),
			filepath.Join(root, "v1", "_other", expiredDay+".ndjson"),
			filepath.Join(root, "notes"),
		}
		for _, p := range kept {
			plant(t, p, data)
		}
		// A directory named like a segment is not a segment.
		dirSegment := accountSegment(root, expiredDay)
		plant(t, filepath.Join(dirSegment, "inner"), data)
		m := mustPrune(t, s, true)
		for _, p := range append(kept, filepath.Join(dirSegment, "inner")) {
			same(t, p, data)
		}
		if m.Deleted != 0 || m.Compressed != 0 {
			t.Errorf("maintenance %+v touched unrelated entries", m)
		}
	})

	t.Run("empty partitions", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		v1 := filepath.Join(root, "v1")
		empty := filepath.Join(v1, "1111111111111111")
		lockOnly := filepath.Join(v1, "2222222222222222")
		emptied := filepath.Join(v1, "3333333333333333")
		other := filepath.Join(v1, "4444444444444444")
		notPartition := filepath.Join(v1, "ABCDEF0123456789")
		for _, d := range []string{empty, notPartition} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		plant(t, filepath.Join(lockOnly, "telemetry.lock"), nil)
		plant(t, filepath.Join(emptied, expiredDay+".ndjson"), content(t))
		plant(t, filepath.Join(other, "keep.txt"), content(t))
		m := mustPrune(t, s, true)
		for _, d := range []string{empty, lockOnly, emptied} {
			if exists(t, d) {
				t.Errorf("empty partition %s kept", filepath.Base(d))
			}
		}
		for _, p := range []string{filepath.Join(other, "keep.txt"), notPartition} {
			if !exists(t, p) {
				t.Errorf("%s removed", filepath.Base(p))
			}
		}
		if m.Deleted != 1 {
			t.Errorf("maintenance %+v, want 1 deleted", m)
		}
	})
}

func TestPruneOpenWindowsFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to delete a file held open")
	}
	s, root, _ := pruneFixture(t, testNow, 30)
	seg := accountSegment(root, "2026-10-01")
	plant(t, seg, quotaLine(t, 1, testNow))
	// os.Open shares the file without delete access.
	f, err := os.Open(seg)
	if err != nil {
		t.Fatal(err)
	}
	m := mustPrune(t, s, true)
	if !exists(t, seg) || m.Deleted != 0 || m.Skipped != 1 {
		t.Errorf("held-open file: %+v, exists %v", m, exists(t, seg))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if m := mustPrune(t, s, true); exists(t, seg) || m.Deleted != 1 {
		t.Errorf("after close: %+v, exists %v", m, exists(t, seg))
	}
}

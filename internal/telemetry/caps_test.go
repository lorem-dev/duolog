package telemetry

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The caps in bytes, with 1 MB = 1,000,000 bytes.
const (
	testDayCapBytes  = 2_000_000 // raw bytes of one project for one UTC day
	testRootCapBytes = 1_000_000 // the whole root with max_megabytes 1
)

const otherStateRoot = "/work/other"

// filler is n bytes of whole lines, none over 1 KiB. The caps count bytes,
// so what the lines hold does not matter.
func filler(n int) []byte {
	var b bytes.Buffer
	for n > 0 {
		k := min(n, 1000)
		b.Write(bytes.Repeat([]byte("x"), k-1))
		b.WriteByte('\n')
		n -= k
	}
	return b.Bytes()
}

// treeBytes adds up the sizes of every entry under root but directories.
func treeBytes(t *testing.T, root string) int64 {
	t.Helper()
	var n int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		n += info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	return mustLstat(t, path).Size()
}

// segmentOf is the segment of the project at stateRoot for day, with the id
// computed from the salt on disk.
func segmentOf(t *testing.T, root, stateRoot, day string) string {
	t.Helper()
	salt, err := os.ReadFile(filepath.Join(root, "salt"))
	if err != nil || len(salt) != 32 {
		t.Fatalf("salt: %d bytes, %v", len(salt), err)
	}
	return filepath.Join(root, "v1", referenceID([32]byte(salt), stateRoot), day+".ndjson")
}

// capFixture opens a store with collection on and a root cap of
// maxMegabytes, on a clock the test can move.
func capFixture(t *testing.T, maxMegabytes int) (s *Store, base, root string, c *clock) {
	t.Helper()
	noOverride(t)
	base = tempBase(t)
	c = &clock{now: testNow}
	o := storeOptions(base)
	o.MaxMegabytes = maxMegabytes
	o.Now = c.Now
	s = mustOpenStore(t, o)
	return s, base, storeRoot(t, base), c
}

func TestDailyCap(t *testing.T) {
	line := mustEncode(t, sampleRun(), testNames)

	t.Run("exactly 2 MB", func(t *testing.T) {
		s, base, root, c := capFixture(t, 16)
		seg := projectSegment(t, root, testDay)
		plant(t, seg, filler(testDayCapBytes-len(line)))
		before := s.Health()
		mustAppend(t, s, sampleRun())
		if got := fileSize(t, seg); got != testDayCapBytes {
			t.Fatalf("segment %d bytes, want %d", got, testDayCapBytes)
		}
		// The next line is dropped whole and counted, not as a failure.
		if err := s.Append(sampleRun()); err != errCapped {
			t.Fatalf("Append over the cap: %v, want %v", err, errCapped)
		}
		data, err := os.ReadFile(seg)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != testDayCapBytes || !bytes.HasSuffix(data, line) {
			t.Errorf("segment %d bytes ending %q, want %d ending in the accepted line", len(data), tail(data), testDayCapBytes)
		}
		if d, f := healthDelta(before, s.Health()); d != 1 || f != 0 {
			t.Errorf("health moved by %d dropped, %d failures; want 1 and 0", d, f)
		}

		// The account shares only the root cap: 2 MB of quota is no limit.
		plant(t, accountSegment(root, testDay), filler(testDayCapBytes))
		mustAppend(t, s, sampleQuota())
		// Another project has its own allowance for the day.
		o := storeOptions(base)
		o.CanonicalStateRoot, o.Now = otherStateRoot, c.Now
		mustAppend(t, mustOpenStore(t, o), sampleRun())
		if got := segmentLines(t, segmentOf(t, root, otherStateRoot, testDay)); len(got) != 1 {
			t.Errorf("the other project holds %d lines, want 1", len(got))
		}
		// So has the next UTC day.
		c.set(testNow.Add(day))
		mustAppend(t, s, sampleRun())
		if got := segmentLines(t, projectSegment(t, root, "2026-11-06")); len(got) != 1 {
			t.Errorf("the next day holds %d lines, want 1", len(got))
		}
		if d, f := healthDelta(before, s.Health()); d != 1 || f != 0 {
			t.Errorf("health moved by %d dropped, %d failures; want 1 and 0", d, f)
		}
	})

	t.Run("repaired tail", func(t *testing.T) {
		partial := []byte(`{"v":1,"t":1793880000,"type":"run","role":"rev`)

		// Whole lines at the cap: cutting a broken tail frees nothing that
		// survives, so the event is still dropped.
		s, _, root, _ := capFixture(t, 16)
		seg := projectSegment(t, root, testDay)
		full := filler(testDayCapBytes)
		plant(t, seg, slices.Concat(full, partial))
		if err := s.Append(sampleRun()); err != errCapped {
			t.Fatalf("Append over the cap: %v, want %v", err, errCapped)
		}
		if got, _ := os.ReadFile(seg); !bytes.Equal(got, full) {
			t.Errorf("segment %d bytes ending %q, want the %d whole bytes", len(got), tail(got), len(full))
		}

		// The cut bytes never counted: whole lines fill the cap exactly.
		s, _, root, _ = capFixture(t, 16)
		seg = projectSegment(t, root, testDay)
		room := filler(testDayCapBytes - len(line))
		plant(t, seg, slices.Concat(room, partial))
		mustAppend(t, s, sampleRun())
		if got, _ := os.ReadFile(seg); !bytes.Equal(got, slices.Concat(room, line)) {
			t.Errorf("segment %d bytes ending %q, want %d ending in the line", len(got), tail(got), testDayCapBytes)
		}
	})

	t.Run("same-day gzip after the clock was set back", func(t *testing.T) {
		s, _, root, _ := capFixture(t, 16)
		seg := projectSegment(t, root, testDay)
		compressed := gzipped(t, filler(1_500_000))
		plant(t, seg+".gz", compressed)
		plant(t, seg, filler(500_000-len(line)))
		// Raw bytes fill the day exactly; on disk it is far below the cap.
		mustAppend(t, s, sampleRun())
		if onDisk := int64(len(compressed)) + fileSize(t, seg); onDisk+int64(len(line)) > testDayCapBytes/2 {
			t.Fatalf("the gzip copy is too large to tell raw from disk bytes: %d", onDisk)
		}
		if err := s.Append(sampleRun()); err != errCapped {
			t.Errorf("Append over the raw cap: %v, want %v", err, errCapped)
		}
		if got := fileSize(t, seg); got != 500_000 {
			t.Errorf("plain segment %d bytes, want 500000", got)
		}
	})
}

func TestRootCap(t *testing.T) {
	line := mustEncode(t, sampleRun(), testNames)

	// fill plants this project's segment for today, which is never evicted,
	// so that the root holds total bytes. The stamp is fresh, so the append
	// runs no prune first.
	fill := func(t *testing.T, root string, total int) string {
		t.Helper()
		setStamp(t, root, testNow)
		seg := projectSegment(t, root, testDay)
		plant(t, seg, nil)
		plant(t, seg, filler(total-int(treeBytes(t, root))))
		if got := treeBytes(t, root); got != int64(total) {
			t.Fatalf("root holds %d bytes, want %d", got, total)
		}
		return seg
	}

	t.Run("evicts the oldest across partitions", func(t *testing.T) {
		s, _, root, _ := capFixture(t, 1)
		mustAppend(t, s, sampleQuota())
		oldest := accountSegment(root, "2026-10-20")
		second := filepath.Join(root, "v1", otherID, "2026-10-21.ndjson.gz")
		newer := accountSegment(root, "2026-10-25")
		for _, p := range []string{oldest, second, newer} {
			plant(t, p, filler(100_000))
		}
		seg := fill(t, root, testRootCapBytes+150_000-len(line))
		mustAppend(t, s, sampleRun())
		if exists(t, oldest) || exists(t, second) {
			t.Errorf("oldest kept: %v, second kept: %v", exists(t, oldest), exists(t, second))
		}
		if !exists(t, newer) || !exists(t, accountSegment(root, testDay)) {
			t.Error("a newer segment was evicted")
		}
		if got, _ := os.ReadFile(seg); !bytes.HasSuffix(got, line) {
			t.Error("the event was not appended")
		}
		if got := treeBytes(t, root); got > testRootCapBytes {
			t.Errorf("root holds %d bytes after the append, cap %d", got, testRootCapBytes)
		}
	})

	t.Run("metadata, locks and temporary files count", func(t *testing.T) {
		s, _, root, _ := capFixture(t, 1)
		lock := filepath.Join(root, "v1", "_account", "telemetry.lock")
		temp := accountSegment(root, "2026-11-04") + ".gz.tmp"
		notes := filepath.Join(root, "notes")
		for _, p := range []string{lock, temp, notes} {
			plant(t, p, filler(100_000))
		}
		oldest := accountSegment(root, "2026-10-20")
		plant(t, oldest, filler(50_000))
		// One byte over the cap, counting the salt, stamp, lock, temporary
		// and unrelated files; segments alone are far below it.
		seg := fill(t, root, testRootCapBytes-len(line)+1)
		mustAppend(t, s, sampleRun())
		if exists(t, oldest) {
			t.Error("the oldest segment was kept: the files beside the segments were not counted")
		}
		for _, p := range []string{lock, temp, notes} {
			if got := fileSize(t, p); got != 100_000 {
				t.Errorf("%s: %d bytes, want it untouched", filepath.Base(p), got)
			}
		}
		if got, _ := os.ReadFile(seg); !bytes.HasSuffix(got, line) {
			t.Error("the event was not appended")
		}
		if got := treeBytes(t, root); got > testRootCapBytes {
			t.Errorf("root holds %d bytes after the append, cap %d", got, testRootCapBytes)
		}
	})

	t.Run("drops an event that cannot fit", func(t *testing.T) {
		s, _, root, _ := capFixture(t, 1)
		oldest := accountSegment(root, "2026-10-20")
		plant(t, oldest, filler(100_000))
		// Even with every older segment gone, over the cap before any event.
		seg := fill(t, root, testRootCapBytes+100_001)
		before, err := os.ReadFile(seg)
		if err != nil {
			t.Fatal(err)
		}
		total := treeBytes(t, root)
		health := s.Health()
		if err := s.Append(sampleRun()); err != errCapped {
			t.Fatalf("Append over the root cap: %v, want %v", err, errCapped)
		}
		if !exists(t, oldest) {
			t.Error("a segment was evicted for an event that could not fit")
		}
		if got, _ := os.ReadFile(seg); !bytes.Equal(got, before) {
			t.Error("the segment changed")
		}
		if got := treeBytes(t, root); got != total {
			t.Errorf("root went from %d to %d bytes", total, got)
		}
		if d, f := healthDelta(health, s.Health()); d != 1 || f != 0 {
			t.Errorf("health moved by %d dropped, %d failures; want 1 and 0", d, f)
		}
		// An account event cannot fit either; no append reports success
		// above the cap.
		if err := s.Append(sampleQuota()); err != errCapped {
			t.Errorf("account Append over the root cap: %v, want %v", err, errCapped)
		}
	})

	t.Run("prune evicts down to the cap", func(t *testing.T) {
		s, _, root, _ := capFixture(t, 1)
		oldest := accountSegment(root, "2026-10-20") + ".gz"
		second := filepath.Join(root, "v1", otherID, "2026-10-22.ndjson.gz")
		third := accountSegment(root, "2026-10-24") + ".gz"
		for _, p := range []string{oldest, second, third} {
			plant(t, p, filler(400_000))
		}
		m := mustPrune(t, s, true)
		if exists(t, oldest) || !exists(t, second) || !exists(t, third) {
			t.Errorf("kept oldest %v, second %v, third %v; want the oldest evicted only",
				exists(t, oldest), exists(t, second), exists(t, third))
		}
		if m.Deleted != 1 || m.Bytes != 400_000 {
			t.Errorf("maintenance %+v, want 1 deleted, 400000 bytes", m)
		}
		if got := treeBytes(t, root); got > testRootCapBytes {
			t.Errorf("root holds %d bytes after prune, cap %d", got, testRootCapBytes)
		}
	})
}

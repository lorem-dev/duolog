package telemetry

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

// purgeFixture writes this project, another project and the account, and
// plants balance state beside the root, where purge must not reach.
func purgeFixture(t *testing.T) (s *Store, base, root string) {
	t.Helper()
	noOverride(t)
	base = tempBase(t)
	s = mustOpenStore(t, storeOptions(base))
	mustAppend(t, s, sampleRun())
	mustAppend(t, s, sampleQuota())
	o := storeOptions(base)
	o.CanonicalStateRoot = otherStateRoot
	mustAppend(t, mustOpenStore(t, o), sampleGate())
	root = storeRoot(t, base)
	plant(t, filepath.Join(balanceDir(root), "samples"), []byte("balance state\n"))
	return s, base, root
}

// balanceDir is where balance keeps its state, next to the telemetry root.
func balanceDir(root string) string { return filepath.Join(filepath.Dir(root), "balance") }

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func eventTypes(events []Event) []string {
	out := []string{}
	for _, e := range events {
		out = append(out, fmt.Sprintf("%T", e))
	}
	return out
}

func TestPurgeScope(t *testing.T) {
	for _, c := range []struct {
		name        string
		configure   func(o *Options)
		writesAfter bool
	}{
		{"here", func(*Options) {}, true},
		{"here with the project off", func(o *Options) { o.ProjectEnabled = false }, false},
		{"here with collection off", func(o *Options) { o.Enabled = false }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, base, root := purgeFixture(t)
			o := storeOptions(base)
			c.configure(&o)
			s := mustOpenStore(t, o)
			here := filepath.Dir(projectSegment(t, root, testDay))
			other := filepath.Dir(segmentOf(t, root, otherStateRoot, testDay))
			account := filepath.Join(root, "v1", "_account")
			kept := map[string]map[string]string{}
			for _, dir := range []string{other, account, balanceDir(root)} {
				kept[dir] = treeState(t, dir)
			}
			salt := readBytes(t, filepath.Join(root, "salt"))

			if err := s.Purge(false); err != nil {
				t.Fatalf("Purge(false): %v", err)
			}
			if exists(t, here) {
				t.Error("this project's partition was kept")
			}
			for dir, state := range kept {
				if got := treeState(t, dir); !reflect.DeepEqual(got, state) {
					t.Errorf("%s changed:\nbefore %v\nafter  %v", filepath.Base(dir), state, got)
				}
			}
			if got := readBytes(t, filepath.Join(root, "salt")); !bytes.Equal(got, salt) {
				t.Error("the salt changed")
			}
			if !exists(t, filepath.Join(root, "prune.stamp")) {
				t.Error("the stamp lock was removed")
			}
			all := mustOpenStore(t, storeOptions(base))
			if got := eventTypes(mustRead(t, all, Query{AllProjects: true})); !slices.Equal(got, []string{"telemetry.Quota", "telemetry.Gate"}) {
				t.Errorf("read %v after the purge, want the account and the other project", got)
			}

			// This store writes again under the same id only while its
			// project collects; off, nothing comes back.
			s.Append(sampleRun())
			if exists(t, here) != c.writesAfter {
				t.Errorf("partition after an append: %v, want %v", exists(t, here), c.writesAfter)
			}
		})
	}

	t.Run("all", func(t *testing.T) {
		s, base, root := purgeFixture(t)
		oldDir := filepath.Dir(projectSegment(t, root, testDay))
		balance := treeState(t, balanceDir(root))
		if err := s.Purge(true); err != nil {
			t.Fatalf("Purge(true): %v", err)
		}
		for _, p := range []string{"v1", "salt"} {
			if exists(t, filepath.Join(root, p)) {
				t.Errorf("%s kept", p)
			}
		}
		if !exists(t, filepath.Join(root, "prune.stamp")) {
			t.Error("the stamp lock was removed")
		}
		if got := treeState(t, balanceDir(root)); !reflect.DeepEqual(got, balance) {
			t.Errorf("balance changed:\nbefore %v\nafter  %v", balance, got)
		}
		if got := mustRead(t, s, Query{AllProjects: true}); len(got) != 0 {
			t.Errorf("read %d events after purging all", len(got))
		}

		// The store opened before the purge writes under a new salt, and a
		// new store finds the same new id.
		mustAppend(t, s, sampleRun())
		newSeg := projectSegment(t, root, testDay)
		if filepath.Dir(newSeg) == oldDir || exists(t, oldDir) {
			t.Errorf("the purged id %s came back", filepath.Base(oldDir))
		}
		mustAppend(t, mustOpenStore(t, storeOptions(base)), samplePeer())
		if got := types(segmentLines(t, newSeg)); !slices.Equal(got, []string{"run", "peer"}) {
			t.Errorf("new partition holds %v, want run, peer", got)
		}
	})

	t.Run("all with collection off", func(t *testing.T) {
		_, base, root := purgeFixture(t)
		o := storeOptions(base)
		o.Enabled = false
		s := mustOpenStore(t, o)
		if err := s.Purge(true); err != nil {
			t.Fatalf("Purge(true): %v", err)
		}
		for _, e := range sampleEvents() {
			if err := s.Append(e); err != nil {
				t.Fatalf("Append(%T) off: %v", e, err)
			}
		}
		for _, p := range []string{"v1", "salt"} {
			if exists(t, filepath.Join(root, p)) {
				t.Errorf("%s exists after purging with collection off", p)
			}
		}
	})

	t.Run("no root", func(t *testing.T) {
		noOverride(t)
		base := tempBase(t)
		o := storeOptions(base)
		o.Enabled = false
		s := mustOpenStore(t, o)
		for _, all := range []bool{false, true} {
			if err := s.Purge(all); err != nil {
				t.Errorf("Purge(%v) with no root: %v", all, err)
			}
		}
		if exists(t, storeRoot(t, base)) {
			t.Error("purge created the root")
		}
	})

	t.Run("links and unrelated files", func(t *testing.T) {
		s, base, root := purgeFixture(t)
		outside := filepath.Join(base, "outside")
		content := []byte("outside bytes\n")
		plant(t, outside, content)
		outsideDir := filepath.Join(base, "outside-dir")
		plant(t, filepath.Join(outsideDir, "2026-11-02.ndjson"), content)
		account := filepath.Join(root, "v1", "_account")
		hard := filepath.Join(account, "2026-11-01.ndjson")
		if err := os.Link(outside, hard); err != nil {
			t.Skipf("no hard links here: %v", err)
		}
		linkedPartition := filepath.Join(root, "v1", otherID)
		plantReparse(t, outsideDir, linkedPartition)
		linkedSegment := filepath.Join(account, "2026-11-03.ndjson")
		symlinkOrSkip(t, outside, linkedSegment)
		unrelated := []string{filepath.Join(account, "notes.txt"), filepath.Join(root, "notes")}
		for _, p := range unrelated {
			plant(t, p, content)
		}
		outsideState := treeState(t, outsideDir)
		outsideInfo := mustLstat(t, outside)

		if err := s.Purge(true); err != nil {
			t.Fatalf("Purge(true): %v", err)
		}
		if exists(t, hard) {
			t.Error("the hard-linked segment name was kept")
		}
		if got := readBytes(t, outside); !bytes.Equal(got, content) || !os.SameFile(mustLstat(t, outside), outsideInfo) {
			t.Errorf("the outside inode changed: %q", got)
		}
		if got := treeState(t, outsideDir); !reflect.DeepEqual(got, outsideState) {
			t.Errorf("purge followed a partition link:\nbefore %v\nafter  %v", outsideState, got)
		}
		for _, p := range append([]string{linkedPartition, linkedSegment}, unrelated...) {
			if !exists(t, p) {
				t.Errorf("%s removed", filepath.Base(p))
			}
		}
		for _, p := range unrelated {
			if got := readBytes(t, p); !bytes.Equal(got, content) {
				t.Errorf("%s changed", filepath.Base(p))
			}
		}
		if exists(t, filepath.Join(root, "salt")) || exists(t, accountSegment(root, testDay)) {
			t.Error("history or salt kept")
		}
	})
}

func TestPurgeOpenWindowsFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to delete a file held open")
	}
	s, _, root := purgeFixture(t)
	seg := accountSegment(root, testDay)
	// os.Open shares the file without delete access.
	f, err := os.Open(seg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(true); err == nil {
		t.Error("Purge(true) succeeded with a segment held open")
	}
	salt := filepath.Join(root, "salt")
	if !exists(t, seg) || !exists(t, salt) {
		t.Errorf("held-open purge lost data: segment %v, salt %v", exists(t, seg), exists(t, salt))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(true); err != nil {
		t.Fatalf("Purge(true) again: %v", err)
	}
	if exists(t, seg) || exists(t, salt) {
		t.Error("the retried purge left history or salt")
	}
}

// TestPurgeRemovedLockIsRetaken covers a locker that waited on a partition
// lock while a purge removed it: the lock it gets is on a file no longer at
// the name. The hook stands in for the purge and replaces the held file.
func TestPurgeRemovedLockIsRetaken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to move a lock file another handle holds open")
	}
	setup := func(t *testing.T, times int) (s *Store, root string, swaps map[string]int) {
		t.Helper()
		s, root = readFixture(t)
		mustAppend(t, s, sampleRun())
		mustAppend(t, s, sampleQuota())
		locks := []string{
			filepath.Join(filepath.Dir(projectSegment(t, root, testDay)), "telemetry.lock"),
			filepath.Join(root, "v1", "_account", "telemetry.lock"),
		}
		moved := filepath.Join(filepath.Dir(root), "moved")
		if err := os.MkdirAll(moved, 0o700); err != nil {
			t.Fatal(err)
		}
		swaps = map[string]int{}
		setRaceHook(t, func(stage string) {
			if stage != "locked" {
				return
			}
			for _, p := range locks {
				if swaps[p] >= times || !heldLock(t, p) {
					continue
				}
				swaps[p]++
				if err := os.Rename(p, filepath.Join(moved, strconv.Itoa(len(swaps))+"-"+strconv.Itoa(swaps[p]))); err != nil {
					t.Fatal(err)
				}
				plant(t, p, nil)
			}
		})
		return s, root, swaps
	}

	t.Run("append retries once", func(t *testing.T) {
		s, root, swaps := setup(t, 1)
		mustAppend(t, s, sampleRun())
		if got := types(segmentLines(t, projectSegment(t, root, testDay))); !slices.Equal(got, []string{"run", "run"}) {
			t.Errorf("project partition %v", got)
		}
		if len(swaps) != 1 {
			t.Errorf("swapped %v, want the project lock once", swaps)
		}
	})

	t.Run("append fails when the lock moves twice", func(t *testing.T) {
		s, root, _ := setup(t, 2)
		seg := projectSegment(t, root, testDay)
		before := readBytes(t, seg)
		if err := s.Append(sampleRun()); err != errUnsafeStore {
			t.Errorf("Append: %v, want %v", err, errUnsafeStore)
		}
		if got := readBytes(t, seg); !bytes.Equal(got, before) {
			t.Error("appended under a moved lock")
		}
	})

	t.Run("read retries once", func(t *testing.T) {
		s, _, swaps := setup(t, 1)
		if got := eventTypes(mustRead(t, s, Query{})); !slices.Equal(got, []string{"telemetry.Quota", "telemetry.Run"}) {
			t.Errorf("read %v", got)
		}
		if len(swaps) != 2 {
			t.Errorf("swapped %v, want both locks once", swaps)
		}
	})

	t.Run("read skips a partition whose lock moves twice", func(t *testing.T) {
		s, _, _ := setup(t, 2)
		if got := mustRead(t, s, Query{}); len(got) != 0 {
			t.Errorf("read %v under moved locks", eventTypes(got))
		}
	})
}

// heldLock reports whether another handle holds path's lock.
func heldLock(t *testing.T, path string) bool {
	t.Helper()
	l := flock.New(path, flock.SetFlag(os.O_RDWR))
	defer l.Close()
	ok, err := l.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	return !ok
}

// maintenanceChild runs in a test-binary process. "writer" appends run and
// quota events from two goroutines, "pruner" prunes and "purger" purges
// here and all in turn. While it holds the global lock each marks base/held,
// which another holder would find; a writer also checks that every project
// partition on disk comes from the salt now on disk.
func maintenanceChild(t *testing.T, base string) {
	mode := os.Getenv(appendChildEnv)
	root := storeRoot(t, base)
	held := filepath.Join(base, "held")
	setRaceHook(t, func(stage string) {
		if stage != "append" && stage != "prune" && stage != "purge" {
			return
		}
		f, err := os.OpenFile(held, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			t.Errorf("split lock: %s found another holder: %v", stage, err)
			return
		}
		f.Close()
		if stage == "append" {
			staleEpochs(t, root)
		}
		time.Sleep(time.Millisecond)
		if err := os.Remove(held); err != nil {
			t.Error(err)
		}
	})
	s, err := Open(storeOptions(base))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	switch mode {
	case "writer":
		var wg sync.WaitGroup
		for g := range 2 {
			wg.Go(func() {
				for i := range 60 {
					if err := s.Append(runAttempt(g*100 + i + 1)); err != nil {
						t.Error(err)
					}
					if err := s.Append(quotaEpoch(int64(i + 1))); err != nil {
						t.Error(err)
					}
				}
			})
		}
		wg.Wait()
	case "pruner":
		for range 40 {
			if _, err := s.Prune(true); err != nil {
				t.Error(err)
			}
		}
	case "purger":
		for i := range 40 {
			if err := s.Purge(i%2 == 1); err != nil {
				t.Error(err)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	fmt.Println("result done")
}

// staleEpochs fails on a project partition whose id does not come from the
// salt now on disk: one written by a store that missed a purge.
func staleEpochs(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "v1"))
	if err != nil {
		return
	}
	salt, _ := os.ReadFile(filepath.Join(root, "salt"))
	for _, e := range entries {
		if !projectIDPattern.MatchString(e.Name()) {
			continue
		}
		if len(salt) != 32 || e.Name() != referenceID([32]byte(salt), testStateRoot) {
			t.Errorf("partition %s is not of the current salt", e.Name())
		}
	}
}

func TestMaintenanceWriterPurgeRace(t *testing.T) {
	if base := os.Getenv(appendTestEnv); base != "" {
		maintenanceChild(t, base)
		return
	}
	noOverride(t)
	base := tempBase(t)
	mustOpenStore(t, storeOptions(base)).Close()
	root := storeRoot(t, base)
	outside := filepath.Join(base, "outside")
	content := []byte("outside bytes\n")
	plant(t, outside, content)
	hard := filepath.Join(root, "v1", "_account", "2026-11-01.ndjson")
	if err := os.MkdirAll(filepath.Dir(hard), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, hard); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	notes := filepath.Join(root, "notes")
	plant(t, notes, content)
	outsideInfo := mustLstat(t, outside)

	type child struct {
		cmd *exec.Cmd
		out *bytes.Buffer
	}
	var procs []child
	for _, mode := range []string{"writer", "writer", "writer", "pruner", "purger"} {
		cmd, out := startAppendChild(t, "TestMaintenanceWriterPurgeRace", base, mode)
		procs = append(procs, child{cmd, out})
	}
	done := make(chan error, len(procs))
	for _, p := range procs {
		go func() { done <- p.cmd.Wait() }()
	}
	deadline := time.After(2 * time.Minute)
	for range procs {
		select {
		case err := <-done:
			if err != nil {
				for _, p := range procs {
					t.Log(p.out)
				}
				t.Fatalf("child: %v", err)
			}
		case <-deadline:
			t.Fatal("deadlock: the children did not finish")
		}
	}
	for _, p := range procs {
		sc := bufio.NewScanner(bytes.NewReader(p.out.Bytes()))
		found := false
		for sc.Scan() {
			found = found || strings.TrimSpace(sc.Text()) == "result done"
		}
		if !found {
			t.Fatalf("no result:\n%s", p.out)
		}
	}

	// Whole lines only, every project partition of the current salt.
	err := filepath.WalkDir(filepath.Join(root, "v1"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".ndjson") {
			segmentLines(t, p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	staleEpochs(t, root)
	if got := readBytes(t, outside); !bytes.Equal(got, content) || !os.SameFile(mustLstat(t, outside), outsideInfo) {
		t.Errorf("the outside inode changed: %q", got)
	}
	if got := readBytes(t, notes); !bytes.Equal(got, content) {
		t.Error("an unrelated file changed")
	}
	if !exists(t, filepath.Join(root, "prune.stamp")) {
		t.Error("the stamp lock was removed")
	}
}

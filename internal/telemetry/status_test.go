package telemetry

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func mustStatus(t *testing.T, s *Store) Status {
	t.Helper()
	st, err := s.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return st
}

func TestStatusFields(t *testing.T) {
	noOverride(t)
	base := tempBase(t)
	o := storeOptions(base)
	o.RetentionDays, o.PruneEveryHours, o.MaxMegabytes = 20, 3, 8
	s := mustOpenStore(t, o)
	root := storeRoot(t, base)
	here := filepath.Dir(projectSegment(t, root, testDay))
	id := filepath.Base(here)

	t.Run("empty store", func(t *testing.T) {
		st := mustStatus(t, s)
		if !st.Enabled || !st.ProjectEnabled || st.Root != root || st.ProjectID != id {
			t.Errorf("status %+v", st)
		}
		if st.Segments != 0 || st.Partitions != 0 || st.OldestDay != "" || !st.LastPrune.IsZero() {
			t.Errorf("empty store reports %d segments, %d partitions, oldest %q, pruned %v",
				st.Segments, st.Partitions, st.OldestDay, st.LastPrune)
		}
		if st.Bytes != treeBytes(t, root) {
			t.Errorf("bytes %d, want %d", st.Bytes, treeBytes(t, root))
		}
		if _, err := os.Lstat(filepath.Join(root, stampName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Status left a prune stamp: %v", err)
		}
	})

	t.Run("no root", func(t *testing.T) {
		noOverride(t)
		nb := tempBase(t)
		no := storeOptions(nb)
		no.Enabled = false
		off := mustOpenStore(t, no)
		st := mustStatus(t, off)
		want := Status{Root: storeRoot(t, nb), RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16,
			Health: off.Health(), Protection: Protection{Refusal: refusalNone}}
		if st != want {
			t.Errorf("status\n got %+v\nwant %+v", st, want)
		}
		if _, err := os.Lstat(storeRoot(t, nb)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Status made the root: %v", err)
		}
	})

	// Two projects and the account. A day with a plain and a gzip copy is
	// one segment; a temporary file and an unrelated one are none.
	plant(t, filepath.Join(here, "2026-11-03.ndjson"), quotaLine(t, 1, testNow))
	plant(t, filepath.Join(here, "2026-11-03.ndjson.gz"), gzipped(t, quotaLine(t, 1, testNow)))
	plant(t, filepath.Join(here, testDay+".ndjson"), quotaLine(t, 2, testNow))
	plant(t, filepath.Join(here, testDay+".ndjson.gz.tmp"), []byte("x"))
	plant(t, filepath.Join(root, "v1", otherID, "2026-11-04.ndjson"), quotaLine(t, 3, testNow))
	plant(t, accountSegment(root, "2026-10-20")+".gz", gzipped(t, quotaLine(t, 4, testNow)))
	plant(t, filepath.Join(root, "v1", "_account", "notes.txt"), []byte("reviewer claude"))
	pruned := time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC)
	plant(t, filepath.Join(root, stampName), nil)
	if err := os.Chtimes(filepath.Join(root, stampName), pruned, pruned); err != nil {
		t.Fatal(err)
	}

	t.Run("facts", func(t *testing.T) {
		st := mustStatus(t, s)
		if !st.Enabled || !st.ProjectEnabled {
			t.Errorf("enabled %v, project enabled %v", st.Enabled, st.ProjectEnabled)
		}
		if st.Root != root || st.ProjectID != id || len(id) != 16 {
			t.Errorf("root %q id %q, want %q and %q", st.Root, st.ProjectID, root, id)
		}
		if st.Segments != 4 || st.Partitions != 3 || st.OldestDay != "2026-10-20" {
			t.Errorf("%d segments, %d partitions, oldest %q; want 4, 3, 2026-10-20",
				st.Segments, st.Partitions, st.OldestDay)
		}
		if st.Bytes != treeBytes(t, root) {
			t.Errorf("bytes %d, want %d", st.Bytes, treeBytes(t, root))
		}
		if !st.LastPrune.Equal(pruned) || st.LastPrune.Location() != time.UTC {
			t.Errorf("last prune %v, want %v", st.LastPrune, pruned)
		}
		if st.RetentionDays != 20 || st.PruneEveryHours != 3 || st.MaxMegabytes != 8 {
			t.Errorf("caps %d days, %d hours, %d MB", st.RetentionDays, st.PruneEveryHours, st.MaxMegabytes)
		}
	})

	t.Run("health since process start", func(t *testing.T) {
		before := s.Health()
		bad := sampleRun()
		bad.Attempt = 0
		s.Append(bad)
		st := mustStatus(t, s)
		if st.Health.Since != processStart || st.Health != s.Health() {
			t.Errorf("health %+v, want the process counters %+v", st.Health, s.Health())
		}
		if d, f := healthDelta(before, st.Health); d != 1 || f != 1 {
			t.Errorf("health counts %d dropped, %d failures; want 1 and 1", d, f)
		}
	})

	t.Run("protection", func(t *testing.T) {
		st := mustStatus(t, s)
		p := st.Protection
		if p.Refusal != refusalNone || p.IndexVerified != indexExcluded(root) {
			t.Errorf("protection %+v", p)
		}
		backup := runtime.GOOS == "darwin" || runtime.GOOS == "linux"
		if p.BackupVerified != backup {
			t.Errorf("backup verified %v, want %v", p.BackupVerified, backup)
		}
		if p.ModeFixed {
			t.Error("mode fixed on a root already 0700")
		}
		if runtime.GOOS == "windows" {
			return
		}
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if st := mustStatus(t, s); !st.Protection.ModeFixed {
			t.Error("a too open root is not reported fixed")
		}
		if info := mustLstat(t, root); info.Mode().Perm() != 0o700 {
			t.Errorf("root mode %v after Status", info.Mode().Perm())
		}
	})

	t.Run("collection off reads only", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			if err := os.Chmod(root, 0o750); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(root, 0o700) })
		}
		oo := o
		oo.Enabled = false
		off := mustOpenStore(t, oo)
		st := mustStatus(t, off)
		if st.Enabled || st.ProjectEnabled || st.ProjectID != id || st.Segments != 4 {
			t.Errorf("status off %+v", st)
		}
		if st.Protection.ModeFixed || st.Protection.BackupVerified {
			t.Errorf("status off wrote protection %+v", st.Protection)
		}
		if runtime.GOOS != "windows" {
			if info := mustLstat(t, root); info.Mode().Perm() != 0o750 {
				t.Errorf("status off changed the root mode to %v", info.Mode().Perm())
			}
		}
	})

	t.Run("project off keeps its id", func(t *testing.T) {
		po := o
		po.ProjectEnabled = false
		st := mustStatus(t, mustOpenStore(t, po))
		if st.ProjectEnabled || st.ProjectID != id {
			t.Errorf("project off: enabled %v, id %q", st.ProjectEnabled, st.ProjectID)
		}
		po.CanonicalStateRoot = ""
		if st := mustStatus(t, mustOpenStore(t, po)); st.ProjectID != "" {
			t.Errorf("no project, id %q", st.ProjectID)
		}
	})

	t.Run("json holds no record text", func(t *testing.T) {
		st := mustStatus(t, s)
		data, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"reviewer", "claude", "codex", testStateRoot, `"type"`, `"epoch"`} {
			if strings.Contains(string(data), text) {
				t.Errorf("status names %q: %s", text, data)
			}
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		want := []string{"bytes", "enabled", "health", "last_prune", "max_megabytes", "oldest_day", "partitions",
			"project_enabled", "project_id", "protection", "prune_every_hours", "retention_days", "root", "segments"}
		if !slices.Equal(keys, want) {
			t.Errorf("status keys\n got %v\nwant %v", keys, want)
		}
		var h, p map[string]json.RawMessage
		if json.Unmarshal(m["health"], &h) != nil || h["dropped"] == nil || h["failures"] == nil || h["since"] == nil || len(h) != 3 {
			t.Errorf("health %s", m["health"])
		}
		if json.Unmarshal(m["protection"], &p) != nil || p["mode_fixed"] == nil || p["backup_verified"] == nil ||
			p["index_verified"] == nil || p["refusal"] == nil || len(p) != 4 {
			t.Errorf("protection %s", m["protection"])
		}
	})

	t.Run("never pruned", func(t *testing.T) {
		epoch := time.Unix(0, 0)
		if err := os.Chtimes(filepath.Join(root, stampName), epoch, epoch); err != nil {
			t.Fatal(err)
		}
		if st := mustStatus(t, s); !st.LastPrune.IsZero() {
			t.Errorf("last prune %v from a stamp no prune completed", st.LastPrune)
		}
	})

	t.Run("refused root", func(t *testing.T) {
		moved := root + ".moved"
		if err := os.Rename(root, moved); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			os.Remove(root)
			os.Rename(moved, root)
		})
		symlinkOrSkip(t, moved, root)
		st, err := s.Status()
		if !errors.Is(err, errUnsafeStore) {
			t.Fatalf("Status on a symlinked root: %v", err)
		}
		noPath(t, err, base)
		if st.Protection.Refusal != refusalSymlink || st.Segments != 0 || st.Bytes != 0 || st.OldestDay != "" {
			t.Errorf("refused status %+v", st)
		}
	})
}

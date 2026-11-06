package telemetry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A file system without hard links, such as exFAT, FAT32 or some SMB shares,
// refuses the link; the stamp is then made in place, as in 1.4.0.
func TestStampWithoutHardLinks(t *testing.T) {
	for _, linkErr := range []error{syscall.ENOTSUP, syscall.EPERM, errors.New("not supported")} {
		t.Run(linkErr.Error(), func(t *testing.T) {
			s, root, _ := pruneFixture(t, testNow, 30)
			old := linkStamp
			linkStamp = func(*os.Root, string, string) error {
				return &os.LinkError{Op: "link", Err: linkErr}
			}
			t.Cleanup(func() { linkStamp = old })
			plant(t, accountSegment(root, "2026-10-01"), quotaLine(t, 1, testNow))
			if m := mustPrune(t, s, false); m.Deleted != 1 {
				t.Errorf("prune without hard links: %+v, want 1 deleted", m)
			}
			if got := stampTime(t, root); !got.Equal(testNow) {
				t.Errorf("stamp dated %v, want %v", got, testNow)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "prune.stamp.new.") {
					t.Errorf("left %q", e.Name())
				}
			}
		})
	}
}

// newStamp links its dated temporary in at the stamp's name and removes the
// temporary after, so for a moment the stamp has two names, both its own. A
// process that appends in that moment finds the store safe: before, its
// one-link check read the stamp as unsafe, the "telemetry: store unsafe" of
// TestMaintenanceWriterPurgeRace on Windows.
func TestRootStampLinkWindow(t *testing.T) {
	noOverride(t)
	base := tempBase(t)
	creator := mustOpenStore(t, storeOptions(base))
	other := mustOpenStore(t, storeOptions(base))
	old := linkStamp
	t.Cleanup(func() { linkStamp = old })
	linked := false
	var appendErr error
	linkStamp = func(r *os.Root, oldname, newname string) error {
		if err := old(r, oldname, newname); err != nil {
			return err
		}
		linked = true
		appendErr = other.Append(sampleRun())
		return nil
	}
	if err := creator.Purge(false); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if !linked {
		t.Skip("no hard links here")
	}
	if appendErr != nil {
		t.Errorf("Append while the stamp has its temporary name: %v", appendErr)
	}
}

// A temporary stamp that stays linked, as on Windows when another process
// held the stamp open while its creator removed the temporary, leaves the
// store safe, and a prune removes it once it is stale. A name outside the
// root still makes the stamp unsafe, with or without a temporary.
func TestRootStampSecondName(t *testing.T) {
	setup := func(t *testing.T) (*Store, string, *clock) {
		t.Helper()
		s, root, c := pruneFixture(t, testNow, 30)
		mustPrune(t, s, true) // makes the stamp
		return s, root, c
	}
	link := func(t *testing.T, oldname, newname string) {
		t.Helper()
		if err := os.Link(oldname, newname); err != nil {
			t.Skipf("no hard links here: %v", err)
		}
	}
	temp := func(root string) string {
		return filepath.Join(root, fmt.Sprintf("prune.stamp.new.%d.0123456789abcdef", testNow.Unix()))
	}
	stamp := func(root string) string { return filepath.Join(root, stampName) }
	outside := func(root string) string { return filepath.Join(filepath.Dir(root), "outside") }

	t.Run("temporary stamp", func(t *testing.T) {
		s, root, c := setup(t)
		link(t, stamp(root), temp(root))
		if err := s.Append(sampleRun()); err != nil {
			t.Errorf("Append with a linked temporary stamp: %v", err)
		}
		c.set(testNow.Add(2 * time.Minute))
		mustPrune(t, s, true)
		if exists(t, temp(root)) {
			t.Error("the stale temporary stamp was kept")
		}
	})
	t.Run("outside link", func(t *testing.T) {
		s, root, _ := setup(t)
		link(t, stamp(root), outside(root))
		if err := s.Append(sampleRun()); !errors.Is(err, errUnsafeStore) {
			t.Errorf("Append with the stamp linked from outside: %v, want %v", err, errUnsafeStore)
		}
	})
	t.Run("outside link and temporary stamp", func(t *testing.T) {
		s, root, _ := setup(t)
		link(t, stamp(root), temp(root))
		link(t, stamp(root), outside(root))
		if err := s.Append(sampleRun()); !errors.Is(err, errUnsafeStore) {
			t.Errorf("Append with the stamp linked from outside: %v, want %v", err, errUnsafeStore)
		}
		if _, err := s.Prune(true); !errors.Is(err, errUnsafeStore) {
			t.Errorf("Prune with the stamp linked from outside: %v, want %v", err, errUnsafeStore)
		}
	})
}

// A process killed between making the temporary stamp and removing it leaves
// it behind; the next pass removes one older than a minute and keeps a newer
// one, which another process may be about to link.
func TestStaleStampTemporaryIsRemoved(t *testing.T) {
	s, root, _ := pruneFixture(t, testNow, 30)
	name := func(at time.Time) string {
		return filepath.Join(root, fmt.Sprintf("prune.stamp.new.%d.0123456789abcdef", at.Unix()))
	}
	stale, fresh := name(testNow.Add(-2*time.Minute)), name(testNow.Add(-10*time.Second))
	ahead := name(testNow.Add(time.Hour))
	other := filepath.Join(root, "prune.stamp.new.unrelated")
	for _, path := range []string{stale, fresh, ahead, other} {
		plant(t, path, nil)
		// The temporary stamp is dated at the epoch before it is linked, so
		// its mtime says nothing about its age.
		if err := os.Chtimes(path, time.Unix(0, 0), time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
	}
	m := mustPrune(t, s, true)
	if exists(t, stale) || exists(t, ahead) {
		t.Errorf("stale temporary stamps kept: %+v", m)
	}
	if !exists(t, fresh) || !exists(t, other) {
		t.Error("a fresh or unrelated file was removed")
	}
	if m.Deleted != 2 {
		t.Errorf("%+v, want 2 deleted", m)
	}
}

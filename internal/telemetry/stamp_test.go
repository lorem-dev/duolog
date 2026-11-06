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

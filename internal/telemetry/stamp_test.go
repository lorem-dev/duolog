package telemetry

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// noRename makes newStamp act as on a system or file system that cannot
// rename without replacing, so it falls back to the link.
func noRename(t *testing.T) {
	t.Helper()
	old := renameStamp
	renameStamp = func(*os.Root, string, string) error { return errors.ErrUnsupported }
	t.Cleanup(func() { renameStamp = old })
}

// stampTemps lists the temporary stamps left in root.
func stampTemps(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "prune.stamp.new.") {
			left = append(left, e.Name())
		}
	}
	return left
}

// oneLink fails unless the stamp passes the one-link check of openReading.
func oneLink(t *testing.T, root string) {
	t.Helper()
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	f, err := openReading(r, stampName)
	if err != nil {
		t.Fatalf("open the stamp: %v", err)
	}
	f.Close()
}

// The stamp is moved in by a rename that refuses to replace, so it never
// has a second name, and a creator that loses the race keeps the stamp that
// won and drops its temporary.
func TestStampRenamed(t *testing.T) {
	linkFails := func(t *testing.T) {
		t.Helper()
		old := linkStamp
		linkStamp = func(*os.Root, string, string) error {
			t.Error("the stamp was linked in, not renamed")
			return errors.ErrUnsupported
		}
		t.Cleanup(func() { linkStamp = old })
	}
	supported := func(t *testing.T, err error) {
		t.Helper()
		if err != nil && !errors.Is(err, fs.ErrExist) {
			t.Skipf("no rename without replace here: %v", err)
		}
	}
	t.Run("new stamp", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		old := renameStamp
		t.Cleanup(func() { renameStamp = old })
		renameStamp = func(r *os.Root, oldname, newname string) error {
			err := old(r, oldname, newname)
			supported(t, err)
			return err
		}
		linkFails(t)
		if err := s.newStamp(); err != nil {
			t.Fatal(err)
		}
		if got := stampTime(t, root); !got.Equal(time.Unix(0, 0)) {
			t.Errorf("new stamp dated %v, want the epoch", got)
		}
		if left := stampTemps(t, root); len(left) != 0 {
			t.Errorf("left %q", left)
		}
		oneLink(t, root)
	})
	t.Run("another creator won", func(t *testing.T) {
		s, root, _ := pruneFixture(t, testNow, 30)
		stamp := filepath.Join(root, stampName)
		old := renameStamp
		t.Cleanup(func() { renameStamp = old })
		renameStamp = func(r *os.Root, oldname, newname string) error {
			plant(t, stamp, nil) // the other creator's stamp, dated now
			if err := os.Chtimes(stamp, testNow, testNow); err != nil {
				t.Fatal(err)
			}
			err := old(r, oldname, newname)
			supported(t, err)
			if err == nil {
				t.Error("the rename replaced the stamp")
			}
			return err
		}
		linkFails(t)
		if err := s.newStamp(); err != nil {
			t.Fatal(err)
		}
		if got := stampTime(t, root); !got.Equal(testNow) {
			t.Errorf("stamp dated %v, want the winner's %v", got, testNow)
		}
		if left := stampTemps(t, root); len(left) != 0 {
			t.Errorf("left %q", left)
		}
		if err := s.Append(sampleRun()); err != nil {
			t.Errorf("Append after losing the race: %v", err)
		}
	})
}

// A file system without hard links, such as exFAT, FAT32 or some SMB shares,
// refuses the link; the stamp is then made in place, as in 1.4.0.
func TestStampWithoutHardLinks(t *testing.T) {
	for _, linkErr := range []error{syscall.ENOTSUP, syscall.EPERM, errors.New("not supported")} {
		t.Run(linkErr.Error(), func(t *testing.T) {
			s, root, _ := pruneFixture(t, testNow, 30)
			noRename(t)
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
			if left := stampTemps(t, root); len(left) != 0 {
				t.Errorf("left %q", left)
			}
		})
	}
}

// creatorRounds is how many fresh roots the creator children race on.
const creatorRounds = 30

// creatorChild runs in a test-binary process: once base/go appears it opens
// the store of each round's root, which has no stamp yet, and appends or
// prunes, which creates the stamp. With "link" it acts as a system that
// cannot rename without replacing, and the stamp it links in keeps two names
// for 20ms.
func creatorChild(t *testing.T, base string) {
	op, how, _ := strings.Cut(os.Getenv(appendChildEnv), " ")
	if how == "link" {
		noRename(t)
		old := linkStamp
		t.Cleanup(func() { linkStamp = old })
		linkStamp = func(r *os.Root, oldname, newname string) error {
			err := old(r, oldname, newname)
			if err == nil {
				time.Sleep(20 * time.Millisecond)
			}
			return err
		}
	}
	plant(t, filepath.Join(base, "ready-"+op), nil)
	deadline := time.Now().Add(time.Minute)
	for !exists(t, filepath.Join(base, "go")) {
		if time.Now().After(deadline) {
			t.Fatal("no go")
		}
		time.Sleep(100 * time.Microsecond)
	}
	for round := range creatorRounds {
		s, err := Open(storeOptions(filepath.Join(base, strconv.Itoa(round))))
		if err != nil {
			t.Fatal(err)
		}
		if op == "append" {
			err = s.Append(sampleRun())
		} else {
			_, err = s.Prune(true)
		}
		if err != nil {
			t.Errorf("round %d: %s: %v", round, op, err)
		}
		s.Close()
	}
	fmt.Println("result done")
}

// Two processes create the missing stamp of a root at the same moment, one
// appending and one pruning; neither fails, and the stamp ends with one name
// and no temporary beside it. With the fallback link the other process waits
// out the stamp's second name.
func TestStampCreatorsRace(t *testing.T) {
	if base := os.Getenv(appendTestEnv); base != "" {
		creatorChild(t, base)
		return
	}
	for _, how := range []string{"rename", "link"} {
		t.Run(how, func(t *testing.T) {
			noOverride(t)
			base := tempBase(t)
			for round := range creatorRounds {
				// The parent makes each salt, so both children share it.
				mustOpenStore(t, storeOptions(filepath.Join(base, strconv.Itoa(round)))).Close()
			}
			type child struct {
				cmd *exec.Cmd
				out *bytes.Buffer
			}
			var procs []child
			for _, op := range []string{"append", "prune"} {
				cmd, out := startAppendChild(t, "TestStampCreatorsRace", base, op+" "+how)
				procs = append(procs, child{cmd, out})
			}
			deadline := time.Now().Add(time.Minute)
			for !exists(t, filepath.Join(base, "ready-append")) || !exists(t, filepath.Join(base, "ready-prune")) {
				if time.Now().After(deadline) {
					t.Fatal("the children did not start")
				}
				time.Sleep(time.Millisecond)
			}
			plant(t, filepath.Join(base, "go"), nil)
			for _, p := range procs {
				if err := p.cmd.Wait(); err != nil {
					t.Fatalf("child: %v\n%s", err, p.out)
				}
				if !strings.Contains(p.out.String(), "result done") {
					t.Fatalf("no result:\n%s", p.out)
				}
			}
			for round := range creatorRounds {
				root := storeRoot(t, filepath.Join(base, strconv.Itoa(round)))
				if left := stampTemps(t, root); len(left) != 0 {
					t.Errorf("round %d: left %q", round, left)
				}
				oneLink(t, root)
			}
		})
	}
}

// A stamp hard-linked from outside the root is refused, after the reader
// waited for a second name to go, also with a temporary stamp beside it, and
// the outside file keeps its date and mode.
func TestStampOutsideLink(t *testing.T) {
	for _, temp := range []bool{false, true} {
		t.Run(fmt.Sprintf("temporary %v", temp), func(t *testing.T) {
			s, root, _ := pruneFixture(t, testNow.Add(-time.Hour), 30)
			mustPrune(t, s, true) // makes the stamp, dated an hour ago
			stamp := filepath.Join(root, stampName)
			outside := filepath.Join(filepath.Dir(root), "outside")
			if err := os.Link(stamp, outside); err != nil {
				t.Skipf("no hard links here: %v", err)
			}
			if temp {
				name := fmt.Sprintf("prune.stamp.new.%d.0123456789abcdef", testNow.Unix())
				if err := os.Link(stamp, filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			}
			before := mustLstat(t, outside)
			waits := 0
			setRaceHook(t, func(stage string) {
				if stage == "stamp linked" {
					waits++
				}
			})
			if err := s.Append(sampleRun()); !errors.Is(err, errUnsafeStore) {
				t.Errorf("Append with the stamp linked from outside: %v, want %v", err, errUnsafeStore)
			}
			if waits != stampOpens-1 {
				t.Errorf("the reader waited %d times, want %d", waits, stampOpens-1)
			}
			if _, err := s.Prune(true); !errors.Is(err, errUnsafeStore) {
				t.Errorf("Prune with the stamp linked from outside: %v, want %v", err, errUnsafeStore)
			}
			after := mustLstat(t, outside)
			if !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
				t.Errorf("the outside file changed: %v %v, was %v %v", after.ModTime(), after.Mode(), before.ModTime(), before.Mode())
			}
		})
	}
}

// On the fallback path a reader that finds the stamp with two links waits,
// and opens it once its creator has removed the temporary name.
func TestStampLinkRetry(t *testing.T) {
	s, root, _ := pruneFixture(t, testNow, 30)
	mustPrune(t, s, true) // makes the stamp
	temp := filepath.Join(root, fmt.Sprintf("prune.stamp.new.%d.0123456789abcdef", testNow.Unix()))
	if err := os.Link(filepath.Join(root, stampName), temp); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	waits := 0
	setRaceHook(t, func(stage string) {
		if stage != "stamp linked" {
			return
		}
		waits++
		if waits == 2 { // the creator removes its temporary late
			if err := os.Remove(temp); err != nil {
				t.Error(err)
			}
		}
	})
	if err := s.Append(sampleRun()); err != nil {
		t.Errorf("Append once the temporary stamp is gone: %v", err)
	}
	if waits != 2 {
		t.Errorf("the reader waited %d times, want 2", waits)
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
		// The temporary stamp is dated at the epoch before it is moved in,
		// so its mtime says nothing about its age.
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

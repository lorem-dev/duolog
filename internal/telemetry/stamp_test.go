package telemetry

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	renameStamp = func(*os.Root, string, string) error { return errRenameUnsupported }
	t.Cleanup(func() { renameStamp = old })
}

// noLink fails the test if newStamp links the stamp in.
func noLink(t *testing.T) {
	t.Helper()
	old := linkStamp
	linkStamp = func(*os.Root, string, string) error {
		t.Error("the stamp was linked in")
		return errors.ErrUnsupported
	}
	t.Cleanup(func() { linkStamp = old })
}

// setWindowsErrors makes the retries read errors as Windows error codes.
func setWindowsErrors(t *testing.T, on bool) {
	t.Helper()
	old := windowsErrors
	windowsErrors = on
	t.Cleanup(func() { windowsErrors = old })
}

// renameSort is how renameFailure sorts err: "unsupported", "exists" or
// "other".
func renameSort(err error) string {
	switch err := renameFailure(err); {
	case errors.Is(err, errRenameUnsupported):
		return "unsupported"
	case errors.Is(err, fs.ErrExist):
		return "exists"
	}
	return "other"
}

// checkRenameSorts fails unless renameFailure sorts each error as want says.
func checkRenameSorts(t *testing.T, want map[error]string) {
	t.Helper()
	for err, sort := range want {
		if got := renameSort(err); got != sort {
			t.Errorf("%v (%d) sorted %s, want %s", err, err, got, sort)
		}
	}
	if renameFailure(nil) != nil {
		t.Error("a rename that succeeded failed")
	}
}

// A rename that fails for any reason but missing support returns its error:
// a permission, I/O or sharing failure never links a second name in, and
// the stamp is not made in place either.
func TestStampRenameFails(t *testing.T) {
	for name, renameErr := range map[string]error{
		"permission": &os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EACCES},
		"io":         syscall.EIO,
		// ERROR_SHARING_VIOLATION on Windows, retried there first.
		"sharing": syscall.Errno(32),
	} {
		t.Run(name, func(t *testing.T) {
			s, root, _ := pruneFixture(t, testNow, 30)
			old := renameStamp
			t.Cleanup(func() { renameStamp = old })
			renameStamp = func(*os.Root, string, string) error { return renameErr }
			noLink(t)
			if err := s.newStamp(); err == nil {
				t.Error("newStamp succeeded after the rename failed")
			}
			if exists(t, filepath.Join(root, stampName)) {
				t.Error("the stamp was made anyway")
			}
			if left := stampTemps(t, root); len(left) != 0 {
				t.Errorf("left %q", left)
			}
		})
	}
}

// Only a rename the system or file system does not support falls back to
// the link.
func TestStampRenameUnsupported(t *testing.T) {
	s, root, _ := pruneFixture(t, testNow, 30)
	old := renameStamp
	t.Cleanup(func() { renameStamp = old })
	renameStamp = func(*os.Root, string, string) error { return renameFailure(errRenameUnsupported) }
	linked := 0
	oldLink := linkStamp
	t.Cleanup(func() { linkStamp = oldLink })
	linkStamp = func(r *os.Root, oldname, newname string) error {
		linked++
		return oldLink(r, oldname, newname)
	}
	if err := s.newStamp(); err != nil {
		t.Fatal(err)
	}
	if linked != 1 {
		t.Errorf("linked %d times, want 1", linked)
	}
	if got := stampTime(t, root); !got.Equal(time.Unix(0, 0)) {
		t.Errorf("stamp dated %v, want the epoch", got)
	}
}

// On Windows a rename that meets a sharing error is tried again a few times;
// access denied and every other error are returned at once, and none of
// them links.
func TestStampRenameRetried(t *testing.T) {
	sharing, lock, denied := syscall.Errno(32), syscall.Errno(33), syscall.Errno(5)
	for _, tc := range []struct {
		name    string
		windows bool
		fails   []error // returned by the first calls; then the rename runs
		always  error   // returned by every call when set
		calls   int
		ok      bool
	}{
		{name: "sharing then done", windows: true, fails: []error{sharing, lock}, calls: 3, ok: true},
		{name: "sharing for good", windows: true, always: sharing, calls: retryAttempts},
		{name: "access denied", windows: true, always: denied, calls: 1},
		{name: "not windows", always: sharing, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, root, _ := pruneFixture(t, testNow, 30)
			setWindowsErrors(t, tc.windows)
			old := renameStamp
			t.Cleanup(func() { renameStamp = old })
			calls := 0
			renameStamp = func(r *os.Root, oldname, newname string) error {
				calls++
				if tc.always != nil {
					return tc.always
				}
				if calls <= len(tc.fails) {
					return tc.fails[calls-1]
				}
				return old(r, oldname, newname)
			}
			noLink(t)
			err := s.newStamp()
			if calls != tc.calls {
				t.Errorf("renamed %d times, want %d", calls, tc.calls)
			}
			if tc.ok && err != nil {
				t.Errorf("newStamp: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("newStamp succeeded")
			}
			if got := exists(t, filepath.Join(root, stampName)); got != tc.ok {
				t.Errorf("stamp there: %v, want %v", got, tc.ok)
			}
		})
	}
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
	// supported skips only where the rename has no support here.
	supported := func(t *testing.T, err error) {
		t.Helper()
		if errors.Is(err, errRenameUnsupported) {
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
			if err != nil {
				t.Errorf("rename: %v", err)
			}
			return err
		}
		noLink(t)
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
			if !errors.Is(err, fs.ErrExist) {
				t.Errorf("rename onto the winner: %v, want %v", err, fs.ErrExist)
			}
			return err
		}
		noLink(t)
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

// The rename acts on the root that was opened and never resolves its path
// again: once an ancestor link points at another tree, the stamp still
// appears in the opened root, and a temporary of the same name in the other
// tree stays where it is. On Windows the rename goes by path (MoveFileEx),
// so it follows the retargeted link; see renameNoReplace.
func TestStampRenameAnchored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows rename resolves the root's path again by design")
	}
	base := tempBase(t)
	opened, other := filepath.Join(base, "opened"), filepath.Join(base, "other")
	tmp := fmt.Sprintf("prune.stamp.new.%d.0123456789abcdef", testNow.Unix())
	for _, dir := range []string{opened, other} {
		if err := os.MkdirAll(filepath.Join(dir, "root"), 0o700); err != nil {
			t.Fatal(err)
		}
		plant(t, filepath.Join(dir, "root", tmp), nil)
	}
	link := filepath.Join(base, "link")
	plantReparse(t, opened, link)
	r, err := os.OpenRoot(filepath.Join(link, "root"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	plantReparse(t, other, link)
	err = renameNoReplace(r, tmp, stampName)
	if errors.Is(err, errRenameUnsupported) {
		t.Skipf("no rename without replace here: %v", err)
	}
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	for _, c := range []struct {
		path string
		want bool
	}{
		{filepath.Join(opened, "root", stampName), true},
		{filepath.Join(opened, "root", tmp), false},
		{filepath.Join(other, "root", stampName), false},
		{filepath.Join(other, "root", tmp), true},
	} {
		if got := exists(t, c.path); got != c.want {
			t.Errorf("%s there: %v, want %v", strings.TrimPrefix(c.path, base), got, c.want)
		}
	}
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

// creatorWait is how long a creator child waits for the other one to reach
// a step before it fails.
const creatorWait = 10 * time.Second

// creatorChild runs in a test-binary process. In each round it opens the
// store of a root that has no stamp yet and appends or prunes, which
// creates the stamp. It leaves marks in base, named kind-round-op, and
// waits for the other child's: "at" when its temporary is made, before it
// publishes it once the other's is made too, so both publish together;
// "lost" when it finds the other's stamp already there; "saw" when it meets
// the stamp with two names. With "link" it acts as a system that cannot
// rename without replacing, and the winner keeps the stamp's two names
// until the other child saw them.
func creatorChild(t *testing.T, base string) {
	op, how, _ := strings.Cut(os.Getenv(appendChildEnv), " ")
	other := map[string]string{"append": "prune", "prune": "append"}[op]
	round := 0
	mark := func(kind string) {
		plant(t, filepath.Join(base, fmt.Sprintf("%s-%d-%s", kind, round, op)), nil)
	}
	await := func(kind string) {
		path := filepath.Join(base, fmt.Sprintf("%s-%d-%s", kind, round, other))
		deadline := time.Now().Add(creatorWait)
		for !exists(t, path) {
			if time.Now().After(deadline) {
				t.Fatalf("round %d: %s had no %q from %s", round, op, kind, other)
			}
			time.Sleep(100 * time.Microsecond)
		}
	}
	unsupportedHere := false
	oldRename := renameStamp
	t.Cleanup(func() { renameStamp = oldRename })
	renameStamp = func(r *os.Root, oldname, newname string) error {
		mark("at")
		await("at")
		if how == "link" {
			return errRenameUnsupported
		}
		err := oldRename(r, oldname, newname)
		unsupportedHere = unsupportedHere || errors.Is(err, errRenameUnsupported)
		if errors.Is(err, fs.ErrExist) {
			mark("lost")
		}
		return err
	}
	oldLink := linkStamp
	t.Cleanup(func() { linkStamp = oldLink })
	linkStamp = func(r *os.Root, oldname, newname string) error {
		err := oldLink(r, oldname, newname)
		switch {
		case err == nil && how == "link":
			await("saw")
		case errors.Is(err, fs.ErrExist):
			mark("lost")
		}
		return err
	}
	setRaceHook(t, func(stage string) {
		if stage == "stamp linked" {
			mark("saw")
		}
	})
	for round = range creatorRounds {
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
	if unsupportedHere {
		fmt.Println("result unsupported")
		return
	}
	fmt.Println("result done")
}

// Two processes create the missing stamp of a root at the same moment, one
// appending and one pruning; neither fails, and the stamp ends with one name
// and no temporary beside it. In every round both publish only once both
// temporaries are made, so exactly one of them loses. With the rename no
// one ever meets the stamp with two names; with the fallback link the loser
// meets them, waits, and opens the stamp once the winner drops its
// temporary.
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
			for _, p := range procs {
				if err := p.cmd.Wait(); err != nil {
					t.Fatalf("child: %v\n%s", err, p.out)
				}
				if strings.Contains(p.out.String(), "result unsupported") {
					t.Skip("no rename without replace here")
				}
				if !strings.Contains(p.out.String(), "result done") {
					t.Fatalf("no result:\n%s", p.out)
				}
			}
			for round := range creatorRounds {
				lost, saw := creatorMarks(t, base, "lost", round), creatorMarks(t, base, "saw", round)
				switch {
				case len(lost) != 1:
					t.Errorf("round %d: lost by %q, want one creator", round, lost)
				case how == "rename" && len(saw) != 0:
					t.Errorf("round %d: %q met a stamp with two names", round, saw)
				case how == "link" && (len(saw) != 1 || saw[0] != lost[0]):
					t.Errorf("round %d: the stamp's two names met by %q, want the loser %q", round, saw, lost[0])
				}
				root := storeRoot(t, filepath.Join(base, strconv.Itoa(round)))
				if left := stampTemps(t, root); len(left) != 0 {
					t.Errorf("round %d: left %q", round, left)
				}
				oneLink(t, root)
			}
		})
	}
}

// creatorMarks lists the creators that left the mark kind in round.
func creatorMarks(t *testing.T, base, kind string, round int) []string {
	t.Helper()
	var ops []string
	for _, op := range []string{"append", "prune"} {
		if exists(t, filepath.Join(base, fmt.Sprintf("%s-%d-%s", kind, round, op))) {
			ops = append(ops, op)
		}
	}
	return ops
}

// A stamp hard-linked from outside the root is refused, after the reader
// waited for a second name to go, and the outside file keeps its date and
// mode. So it is with a temporary stamp beside it that is a third name of
// the stamp, and with one that is an unrelated file while the stamp has
// exactly two links, the count a creator's link would give it.
func TestStampOutsideLink(t *testing.T) {
	for _, temp := range []string{"none", "linked", "unrelated"} {
		t.Run("temporary "+temp, func(t *testing.T) {
			s, root, _ := pruneFixture(t, testNow.Add(-time.Hour), 30)
			mustPrune(t, s, true) // makes the stamp, dated an hour ago
			stamp := filepath.Join(root, stampName)
			outside := filepath.Join(filepath.Dir(root), "outside")
			if err := os.Link(stamp, outside); err != nil {
				t.Skipf("no hard links here: %v", err)
			}
			tmp := filepath.Join(root, fmt.Sprintf("prune.stamp.new.%d.0123456789abcdef", testNow.Unix()))
			switch temp {
			case "linked":
				if err := os.Link(stamp, tmp); err != nil {
					t.Fatal(err)
				}
			case "unrelated":
				plant(t, tmp, nil)
				epoch := time.Unix(0, 0)
				if err := os.Chtimes(tmp, epoch, epoch); err != nil {
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

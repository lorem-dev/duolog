package telemetry

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	testID      = "0123456789abcdef"
	otherID     = "fedcba9876543210"
	testSegment = "v1/" + testID + "/2026-11-05.ndjson"
)

// tempBase is a temporary directory with its symlinks resolved, so paths
// compare with what the checks resolve (macOS: /var is /private/var).
func tempBase(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return base
}

func mustOpenProtected(t *testing.T, root string) *os.Root {
	t.Helper()
	r, p, err := openProtected(root)
	if err != nil {
		t.Fatalf("openProtected: %v (refusal %s)", err, p.Refusal)
	}
	if p.Refusal != refusalNone {
		t.Fatalf("refusal %q on success", p.Refusal)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func setRaceHook(t *testing.T, hook func(stage string)) {
	t.Helper()
	raceHook = hook
	t.Cleanup(func() { raceHook = nil })
}

// noPath fails when an error or refusal names a path under base.
func noPath(t *testing.T, err error, base string) {
	t.Helper()
	if err != nil && (strings.Contains(err.Error(), base) || strings.Contains(err.Error(), string(filepath.Separator))) {
		t.Errorf("error names a path: %q", err)
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
}

func TestRootProtection(t *testing.T) {
	noOverride(t)

	t.Run("missing", func(t *testing.T) {
		p, err := checkRoot(filepath.Join(tempBase(t), "absent"))
		if err == nil || p.Refusal != refusalOther {
			t.Fatalf("got %+v, %v; want other", p, err)
		}
	})

	t.Run("not a directory", func(t *testing.T) {
		root := filepath.Join(tempBase(t), "telemetry")
		if err := os.WriteFile(root, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := checkRoot(root)
		if err == nil || p.Refusal != refusalOther {
			t.Fatalf("got %+v, %v; want other", p, err)
		}
		if _, p, err := openProtected(root); err == nil || p.Refusal != refusalOther {
			t.Fatalf("openProtected: got %+v, %v; want other", p, err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		base := tempBase(t)
		real := filepath.Join(base, "real")
		if err := os.Mkdir(real, 0o700); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(base, "telemetry")
		symlinkOrSkip(t, real, root)
		p, err := checkRoot(root)
		if err == nil || p.Refusal != refusalSymlink {
			t.Fatalf("got %+v, %v; want symlink", p, err)
		}
		noPath(t, err, base)
		r, p, err := openProtected(root)
		if err == nil || r != nil || p.Refusal != refusalSymlink {
			t.Fatalf("openProtected: got %v, %+v, %v; want symlink", r, p, err)
		}
	})

	t.Run("wrong owner", func(t *testing.T) {
		base := tempBase(t)
		root := filepath.Join(base, "telemetry")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		loosen(t, root)
		fakeForeignOwner(t)
		p, err := checkRoot(root)
		if err == nil || p.Refusal != refusalOwner {
			t.Fatalf("got %+v, %v; want owner", p, err)
		}
		noPath(t, err, base)
		// The owner check comes before any repair.
		if p.ModeFixed || !isLoose(t, root) {
			t.Errorf("a foreign root was repaired: %+v", p)
		}
		if _, p, err := openProtected(root); err == nil || p.Refusal != refusalOwner {
			t.Fatalf("openProtected: got %+v, %v; want owner", p, err)
		}
	})

	t.Run("too open is repaired and reported", func(t *testing.T) {
		root := filepath.Join(tempBase(t), "telemetry")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		loosen(t, root)
		p, err := checkRoot(root)
		if err != nil || p.Refusal != refusalNone || !p.ModeFixed {
			t.Fatalf("got %+v, %v; want a reported repair", p, err)
		}
		assertTight(t, root, true)
		if p, err := checkRoot(root); err != nil || p.ModeFixed {
			t.Fatalf("second check: got %+v, %v; want no repair", p, err)
		}
	})

	t.Run("explicit modes", func(t *testing.T) {
		root := filepath.Join(tempBase(t), "duolog", "telemetry")
		// A umask that would leave the owner without write or search bits.
		withUmask(t, 0o277)
		r := mustOpenProtected(t, root)
		assertTight(t, root, true)
		for _, name := range []string{"salt", testSegment, "v1/_account/2026-11-05.ndjson"} {
			f, err := openRegular(r, name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			f.Close()
			assertTight(t, filepath.Join(root, filepath.FromSlash(name)), false)
		}
		for _, dir := range []string{"v1", "v1/" + testID, "v1/_account"} {
			assertTight(t, filepath.Join(root, filepath.FromSlash(dir)), true)
		}

		// A file left too open is tightened before it is written.
		stamp := filepath.Join(root, "prune.stamp")
		if err := os.WriteFile(stamp, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		loosen(t, stamp)
		f, err := openRegular(r, "prune.stamp", os.O_WRONLY)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		assertTight(t, stamp, false)
	})

	t.Run("root swapped between opens", func(t *testing.T) {
		base := tempBase(t)
		root := filepath.Join(base, "telemetry")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		var swapErr error
		setRaceHook(t, func(stage string) {
			if stage != "root" {
				return
			}
			if swapErr = os.Rename(root, root+".old"); swapErr != nil {
				return
			}
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
		})
		r, p, err := openProtected(root)
		if r != nil {
			defer r.Close()
		}
		switch {
		case heldDirLocked(swapErr):
			// Windows refuses to rename a directory duolog holds open, so
			// the swap cannot happen: the root opens and stays in place.
			if err != nil || r == nil || p.Refusal != refusalNone {
				t.Fatalf("got %v, %+v, %v after a refused swap; want the root opened", r, p, err)
			}
			if err := sameDir(r, root); err != nil {
				t.Errorf("the held root moved: %v", err)
			}
			if entries, _ := os.ReadDir(base); len(entries) != 1 || entries[0].Name() != "telemetry" {
				t.Errorf("entries beside the root: %v, want only telemetry", entries)
			}
			return
		case runtime.GOOS == "windows":
			t.Fatalf("renaming the held root: got %v, want a sharing violation", swapErr)
		case swapErr != nil:
			t.Fatal(swapErr)
		}
		if err == nil || r != nil || p.Refusal != refusalOther {
			t.Fatalf("got %v, %+v, %v; want a refused swap", r, p, err)
		}
		noPath(t, err, base)
	})
}

func TestRootRefusals(t *testing.T) {
	noOverride(t)

	mkdir := func(t *testing.T, dirs ...string) {
		t.Helper()
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	refused := func(t *testing.T, base, root, want string) {
		t.Helper()
		r, p, err := openProtected(root)
		if err == nil || r != nil || p.Refusal != want {
			t.Fatalf("openProtected(%s): got %+v, %v; want %s", root, p, err, want)
		}
		noPath(t, err, base)
		if _, err := os.Lstat(root); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a refused root was created: %v", err)
		}
		// The same checks on a root that already exists.
		mkdir(t, root)
		if p, err := checkRoot(root); err == nil || p.Refusal != want {
			t.Fatalf("checkRoot(%s): got %+v, %v; want %s", root, p, err, want)
		}
	}

	t.Run("git directory", func(t *testing.T) {
		base := tempBase(t)
		mkdir(t, filepath.Join(base, "repo", ".git"))
		refused(t, base, filepath.Join(base, "repo", "nested", "duolog", "telemetry"), refusalGit)
	})

	t.Run("git worktree marker", func(t *testing.T) {
		base := tempBase(t)
		mkdir(t, filepath.Join(base, "wt"))
		if err := os.WriteFile(filepath.Join(base, "wt", ".git"), []byte("gitdir: ../repo/.git/worktrees/wt\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		refused(t, base, filepath.Join(base, "wt", "duolog", "telemetry"), refusalGit)
	})

	t.Run("git through a symlink", func(t *testing.T) {
		base := tempBase(t)
		mkdir(t, filepath.Join(base, "repo", ".git"))
		symlinkOrSkip(t, filepath.Join(base, "repo"), filepath.Join(base, "alias"))
		refused(t, base, filepath.Join(base, "alias", "duolog", "telemetry"), refusalGit)
	})

	synced := []string{
		filepath.Join("Library", "Mobile Documents"),
		filepath.Join("Library", "CloudStorage", "Provider"),
		"Dropbox",
		"OneDrive",
		"Google Drive",
		"OneDrive - Example",
		"Dropbox (Example)",
	}
	for _, dir := range synced {
		t.Run("synced "+dir, func(t *testing.T) {
			base := tempBase(t)
			refused(t, base, filepath.Join(base, dir, "duolog", "telemetry"), refusalSync)
		})
	}

	t.Run("synced through a symlink", func(t *testing.T) {
		base := tempBase(t)
		mkdir(t, filepath.Join(base, "Dropbox"))
		symlinkOrSkip(t, filepath.Join(base, "Dropbox"), filepath.Join(base, "plain"))
		refused(t, base, filepath.Join(base, "plain", "duolog", "telemetry"), refusalSync)
	})

	near := []string{
		"Dropbox-archive",
		"MyOneDrive",
		"OneDriveNotes",
		"Google Drive notes",
		"Google",
		filepath.Join("Library", "Mobile"),
		"Mobile Documents",
		"CloudStorage",
		filepath.Join("Library", "Application Support"),
		"gitwork",
	}
	for _, dir := range near {
		t.Run("ordinary "+dir, func(t *testing.T) {
			base := tempBase(t)
			mkdir(t, filepath.Join(base, dir, ".github"))
			if err := os.WriteFile(filepath.Join(base, dir, ".gitignore"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			mustOpenProtected(t, filepath.Join(base, dir, "duolog", "telemetry"))
		})
	}
}

func TestProtectedOpen(t *testing.T) {
	noOverride(t)
	base := tempBase(t)
	root := filepath.Join(base, "telemetry")
	r := mustOpenProtected(t, root)
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	at := func(name string) string { return filepath.Join(root, filepath.FromSlash(name)) }

	t.Run("valid names", func(t *testing.T) {
		for _, name := range []string{
			"salt", "prune.stamp", "CACHEDIR.TAG",
			"v1/_account/2026-11-05.ndjson", "v1/_account/telemetry.lock",
			testSegment, "v1/" + testID + "/2026-11-04.ndjson.gz", "v1/" + testID + "/2026-11-04.ndjson.gz.tmp",
			"v1/" + testID + "/telemetry.lock",
		} {
			f, err := openRegular(r, name, os.O_RDWR|os.O_CREATE)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			f.Close()
		}
	})

	t.Run("invalid id or date", func(t *testing.T) {
		for _, name := range []string{
			"", ".", "..", "/salt", "../salt", "./salt", "v1/../salt", "salt/x", "v1",
			"v1/" + testID, "v1/" + testID + "/", "v2/" + testID + "/2026-11-05.ndjson",
			"v1/0123456789ABCDEF/2026-11-05.ndjson", "v1/0123456789abcde/2026-11-05.ndjson",
			"v1/0123456789abcdef0/2026-11-05.ndjson", "v1/0123456789abcdeg/2026-11-05.ndjson",
			"v1/account/2026-11-05.ndjson", "v1/" + testID + "/2026-13-01.ndjson",
			"v1/" + testID + "/2026-02-30.ndjson", "v1/" + testID + "/26-11-05.ndjson",
			"v1/" + testID + "/2026-11-5.ndjson", "v1/" + testID + "/2026-11-05.json",
			"v1/" + testID + "/2026-11-05.ndjson.tmp", "v1/" + testID + "/x/2026-11-05.ndjson",
			"v1\\" + testID + "\\2026-11-05.ndjson", "v1//" + testID + "/2026-11-05.ndjson",
			"SALT", "salt\x00", "v1/" + testID + "/2026-11-05.ndjson\n",
		} {
			f, err := openRegular(r, name, os.O_RDWR|os.O_CREATE)
			if !errors.Is(err, errBadName) {
				t.Errorf("%q: got %v, want errBadName", name, err)
			}
			if f != nil {
				f.Close()
			}
		}
	})

	t.Run("traversal", func(t *testing.T) {
		// A v1 that leaves the root is refused, and nothing lands outside.
		sub := filepath.Join(tempBase(t), "telemetry")
		rs := mustOpenProtected(t, sub)
		symlinkOrSkip(t, outside, filepath.Join(sub, "v1"))
		if _, err := openRegular(rs, testSegment, os.O_WRONLY|os.O_CREATE); err == nil {
			t.Fatal("opened through a symlinked v1")
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("wrote outside the root: %v", entries)
		}
	})

	t.Run("partition symlink", func(t *testing.T) {
		if err := os.MkdirAll(at("v1/"+otherID), 0o700); err != nil {
			t.Fatal(err)
		}
		link := at("v1/" + testID)
		os.RemoveAll(link)
		symlinkOrSkip(t, otherID, link)
		defer os.Remove(link)
		f, err := openRegular(r, testSegment, os.O_WRONLY|os.O_CREATE)
		if !errors.Is(err, errUnsafe) {
			t.Fatalf("got %v, want errUnsafe", err)
		}
		if f != nil {
			f.Close()
		}
		if _, err := os.Lstat(at("v1/" + otherID + "/2026-11-05.ndjson")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("wrote through the partition symlink: %v", err)
		}
	})

	t.Run("file symlink", func(t *testing.T) {
		if err := os.MkdirAll(at("v1/"+testID), 0o700); err != nil {
			t.Fatal(err)
		}
		target := at("v1/" + testID + "/2026-11-04.ndjson")
		if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		link := at(testSegment)
		os.Remove(link)
		symlinkOrSkip(t, "2026-11-04.ndjson", link)
		defer os.Remove(link)
		if _, err := openRegular(r, testSegment, os.O_WRONLY|os.O_TRUNC); !errors.Is(err, errUnsafe) {
			t.Fatalf("got %v, want errUnsafe", err)
		}
		if got, _ := os.ReadFile(target); string(got) != "keep\n" {
			t.Errorf("target changed: %q", got)
		}
	})

	t.Run("reparse point", func(t *testing.T) {
		link := at("v1/" + otherID)
		os.RemoveAll(link)
		plantReparse(t, outside, link)
		defer os.Remove(link)
		f, err := openRegular(r, "v1/"+otherID+"/2026-11-05.ndjson", os.O_WRONLY|os.O_CREATE)
		if !errors.Is(err, errUnsafe) {
			t.Fatalf("got %v, want errUnsafe", err)
		}
		if f != nil {
			f.Close()
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("wrote through the reparse point: %v", entries)
		}
	})

	t.Run("not a regular file", func(t *testing.T) {
		name := "v1/" + testID + "/2026-11-03.ndjson"
		if err := os.MkdirAll(at(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := openRegular(r, name, os.O_RDONLY); !errors.Is(err, errUnsafe) {
			t.Fatalf("got %v, want errUnsafe", err)
		}
	})

	t.Run("replacement between checks", func(t *testing.T) {
		if err := os.MkdirAll(at("v1/"+testID), 0o700); err != nil {
			t.Fatal(err)
		}
		os.Remove(at(testSegment))
		if err := os.WriteFile(at(testSegment), []byte("checked\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		swap := at("v1/" + testID + "/2026-11-02.ndjson")
		if err := os.WriteFile(swap, []byte("swapped\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		setRaceHook(t, func(stage string) {
			if stage == "file" {
				if err := os.Rename(swap, at(testSegment)); err != nil {
					t.Fatal(err)
				}
			}
		})
		f, err := openRegular(r, testSegment, os.O_WRONLY|os.O_TRUNC)
		if !errors.Is(err, errUnsafe) {
			t.Fatalf("got %v, want errUnsafe", err)
		}
		if f != nil {
			f.Close()
		}
		if got, _ := os.ReadFile(at(testSegment)); string(got) != "swapped\n" {
			t.Errorf("the replacement was truncated: %q", got)
		}
	})

	t.Run("no path in errors", func(t *testing.T) {
		_, err := openRegular(r, "v1/"+testID+"/2026-10-01.ndjson", os.O_RDONLY)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("got %v, want not exist", err)
		}
		noPath(t, err, base)
	})
}

func TestHardLinksNeverMutateOutside(t *testing.T) {
	noOverride(t)
	base := tempBase(t)
	root := filepath.Join(base, "telemetry")
	r := mustOpenProtected(t, root)
	for _, dir := range []string{"v1/" + testID, "v1/_account"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{
		"salt", "prune.stamp", "CACHEDIR.TAG",
		testSegment, "v1/_account/2026-11-05.ndjson",
		"v1/" + testID + "/telemetry.lock",
		"v1/" + testID + "/2026-11-04.ndjson.gz.tmp",
	}
	flags := []int{
		os.O_RDONLY, os.O_RDWR, os.O_WRONLY | os.O_APPEND, os.O_WRONLY | os.O_TRUNC,
		os.O_RDWR | os.O_CREATE | os.O_TRUNC, os.O_WRONLY | os.O_CREATE | os.O_APPEND,
	}
	content := []byte("outside bytes\n")
	outsides := map[string]os.FileMode{}
	for _, name := range names {
		outside := filepath.Join(base, "outside-"+strings.ReplaceAll(name, "/", "-"))
		if err := os.WriteFile(outside, content, 0o640); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(outside)
		if err != nil {
			t.Fatal(err)
		}
		outsides[outside] = before.Mode()
		// The link replaces a file already there, such as the Linux
		// CACHEDIR.TAG, the way an attacker would: link beside it, then
		// rename over it.
		at := filepath.Join(root, filepath.FromSlash(name))
		if err := os.Link(outside, at+".link"); err != nil {
			t.Fatalf("%s: planting the hard link: %v", name, err)
		}
		if err := os.Rename(at+".link", at); err != nil {
			t.Fatalf("%s: renaming the hard link into place: %v", name, err)
		}
		for _, flag := range flags {
			f, err := openRegular(r, name, flag)
			if !errors.Is(err, errUnsafe) {
				t.Errorf("%s flags %#x: got %v, want errUnsafe", name, flag, err)
			}
			if f != nil {
				f.Write([]byte("x"))
				f.Close()
			}
		}
	}
	// The backup exclusion writes beside the links, never through them.
	excludeBackup(root)
	for outside, mode := range outsides {
		after, err := os.Stat(outside)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(outside); !bytes.Equal(got, content) {
			t.Errorf("%s: outside bytes changed to %q", outside, got)
		}
		if after.Mode() != mode {
			t.Errorf("%s: outside mode changed from %v to %v", outside, mode, after.Mode())
		}
	}
	again, p, err := openProtected(root)
	if err != nil || p.Refusal != refusalNone {
		t.Errorf("root after the links: %+v, %v", p, err)
	}
	if again != nil {
		again.Close()
	}
	// No handle stays open: on Windows an open one keeps the root from
	// being removed, and the temporary directory cleanup fails without
	// saying why.
	r.Close()
	if err := os.RemoveAll(root); err != nil {
		t.Errorf("removing the root after closing every handle: %v", err)
	}
}

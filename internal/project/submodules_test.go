package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func traceGit(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Git process tracing uses a shell wrapper")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	trace := filepath.Join(dir, "calls")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nprintf 'git\\n' >> " + quote(trace) + "\nexec " + quote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return trace
}

func TestDuologModuleSkipsGitWithoutConfig(t *testing.T) {
	f := submodule.New(t)
	trace := traceGit(t)
	w := submoduleWalk{ctx: context.Background(), home: filepath.Join(f.Superproject, ".duolog")}
	if w.duologModule(f.Superproject, f.Submodule) {
		t.Fatal("module without config accepted")
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("Git ran before checking config: %v", err)
	}
}

func TestSubmoduleGitHelpersRespectCancellation(t *testing.T) {
	f := submodule.New(t)
	writeLeadConfig(t, f.Superproject)
	writeLeadConfig(t, f.Submodule)
	trace := traceGit(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := submoduleWalk{ctx: ctx, home: filepath.Join(f.Superproject, ".duolog")}
	if w.duologModule(f.Superproject, f.Submodule) {
		t.Fatal("cancelled walk accepted module")
	}
	if _, err := Checkouts(ctx, f.Linked); err == nil {
		t.Fatal("cancelled checkout enumeration succeeded")
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("Git started after cancellation: %v", err)
	}
}

// submoduleTree is a fixture with a spaced module path, a module with no
// duolog config, and a superproject checkout whose modules are uninitialized.
type submoduleTree struct {
	submodule.Fixture
	Spaced, SpacedLinked, Vendor, Uninitialized string
}

func newSubmoduleTree(t *testing.T) submoduleTree {
	t.Helper()
	f := submoduleTree{Fixture: submodule.New(t)}
	for _, root := range []string{f.Superproject, f.Submodule, f.Nested, f.SuperLinkedSubmodule, f.Unrelated} {
		writeLeadConfig(t, root)
	}
	base := filepath.Dir(f.Superproject)
	for _, name := range []string{"spaced-source", "vendor-source"} {
		submodule.Init(t, filepath.Join(base, name))
	}
	submodule.Git(t, f.Superproject, "submodule", "add", "-q", filepath.Join(base, "spaced-source"), "with space")
	submodule.Git(t, f.Superproject, "submodule", "add", "-q", filepath.Join(base, "vendor-source"), "vendor")
	submodule.Git(t, f.Superproject, "commit", "-q", "-m", "Add modules")
	f.Spaced = filepath.Join(f.Superproject, "with space")
	f.Vendor = filepath.Join(f.Superproject, "vendor")
	writeLeadConfig(t, f.Spaced)
	f.SpacedLinked = filepath.Join(f.Spaced, ".worktrees", "feature one")
	submodule.Git(t, f.Spaced, "worktree", "add", "-q", "--detach", f.SpacedLinked)
	f.Uninitialized = filepath.Join(base, "uninitialized")
	submodule.Git(t, f.Superproject, "worktree", "add", "-q", "--detach", f.Uninitialized)
	return f
}

func canonicalSet(t *testing.T, paths []string) []string {
	t.Helper()
	var out []string
	for _, p := range paths {
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, resolved)
	}
	slices.Sort(out)
	return out
}

func TestDuologSubmodules(t *testing.T) {
	f := newSubmoduleTree(t)
	want := canonicalSet(t, []string{f.Submodule, f.Linked, f.Nested, f.SuperLinkedSubmodule, f.Spaced, f.SpacedLinked})
	ctx := context.Background()
	alias := filepath.Join(t.TempDir(), "alias")
	roots := []string{f.Superproject, f.SuperLinked, f.Uninitialized}
	if err := os.Symlink(f.Superproject, alias); err == nil {
		roots = append(roots, alias)
	}
	for _, root := range roots {
		got, err := DuologSubmodules(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
			t.Fatalf("unsorted or repeated result %q", got)
		}
		if have := canonicalSet(t, got); !slices.Equal(have, want) {
			t.Fatalf("from %s:\n got %q\nwant %q", root, have, want)
		}
	}
	t.Run("hostile git environment", func(t *testing.T) {
		t.Setenv("GIT_DIR", filepath.Join(f.Unrelated, ".git"))
		t.Setenv("GIT_WORK_TREE", f.Unrelated)
		got, err := DuologSubmodules(ctx, f.Superproject)
		if err != nil || !slices.Equal(canonicalSet(t, got), want) {
			t.Fatalf("%q (%v)", got, err)
		}
	})
	t.Run("no git or no modules", func(t *testing.T) {
		for _, root := range []string{t.TempDir(), f.Unrelated} {
			if got, err := DuologSubmodules(ctx, root); err != nil || len(got) != 0 {
				t.Fatalf("%s: %q (%v)", root, got, err)
			}
		}
	})
	t.Run("malformed gitmodules", func(t *testing.T) {
		file := filepath.Join(f.Superproject, ".gitmodules")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("[submodule \"x\"\n\tpath = \"unterminated\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(file, data, 0o600) })
		if _, err := DuologSubmodules(ctx, f.Superproject); err == nil {
			t.Fatal("malformed .gitmodules accepted")
		}
	})
}

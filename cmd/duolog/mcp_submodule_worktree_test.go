package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

// submoduleWorktrees is a submodule fixture with duolog config in the
// superproject, its configured modules and an unrelated nested repository,
// and named linked checkouts under .worktrees.
type submoduleWorktrees struct {
	submodule.Fixture
	Feature, Spaced, SuperDup, SubDup, Vendor, Uninitialized, UnrelatedOnly string
}

func newSubmoduleWorktrees(t *testing.T) submoduleWorktrees {
	t.Helper()
	f := submoduleWorktrees{Fixture: submodule.New(t)}
	for _, root := range []string{f.Superproject, f.Submodule, f.SuperLinkedSubmodule, f.Unrelated} {
		writeSubmoduleConfig(t, root)
	}
	base := filepath.Dir(f.Superproject)
	submodule.Init(t, filepath.Join(base, "vendor-source"))
	submodule.Git(t, f.Superproject, "submodule", "add", "-q", filepath.Join(base, "vendor-source"), "vendor")
	submodule.Git(t, f.Superproject, "commit", "-q", "-m", "Add vendor")
	f.Vendor = filepath.Join(f.Superproject, "vendor")
	f.Feature = filepath.Join(f.Submodule, ".worktrees", "feature")
	f.Spaced = filepath.Join(f.Submodule, ".worktrees", "feature one")
	f.SubDup = filepath.Join(f.Submodule, ".worktrees", "dup")
	for _, dir := range []string{f.Feature, f.Spaced, f.SubDup} {
		submodule.Git(t, f.Submodule, "worktree", "add", "-q", "--detach", dir)
	}
	f.SuperDup = filepath.Join(f.Superproject, ".worktrees", "dup")
	submodule.Git(t, f.Superproject, "worktree", "add", "-q", "--detach", f.SuperDup)
	f.Uninitialized = filepath.Join(base, "uninitialized")
	submodule.Git(t, f.Superproject, "worktree", "add", "-q", "--detach", f.Uninitialized)
	f.UnrelatedOnly = filepath.Join(f.Unrelated, ".worktrees", "only-unrelated")
	submodule.Git(t, f.Unrelated, "worktree", "add", "-q", "--detach", f.UnrelatedOnly)
	return f
}

func writeSubmoduleConfig(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sameFile(t *testing.T, got, want string) {
	t.Helper()
	a, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(want)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(a, b) {
		t.Fatalf("resolved %s, want %s", got, want)
	}
}

func TestMCPSubmoduleWorktreeResolution(t *testing.T) {
	f := newSubmoduleWorktrees(t)
	t.Chdir(f.Superproject)
	for _, tc := range []struct{ name, ref, want string }{
		{"absolute linked submodule checkout", f.Linked, f.Linked},
		{"absolute submodule", f.Submodule, f.Submodule},
		{"submodule in a linked superproject checkout", f.SuperLinkedSubmodule, f.SuperLinkedSubmodule},
		{"relative path from the server", "module/.worktrees/feature", f.Feature},
		{"bare name in a submodule", "feature", f.Feature},
		{"bare name with a space", "feature one", f.Spaced},
		{"the server checkout", ".", f.Superproject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := mcpPeerWorktree(tc.ref)
			if err != nil {
				t.Fatal(err)
			}
			sameFile(t, resolved, tc.want)
		})
	}
	if !repositoryCheckout(f.Superproject, f.Linked) {
		t.Fatal("linked submodule")
	}
	if repositoryCheckout(f.Superproject, f.Unrelated) {
		t.Fatal("unrelated repo")
	}
	t.Run("symlink alias", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(f.Superproject, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		resolved, err := mcpPeerWorktree(filepath.Join(alias, "module"))
		if err != nil {
			t.Fatal(err)
		}
		sameFile(t, resolved, f.Submodule)
	})
	t.Run("duplicate bare name", func(t *testing.T) {
		_, ambiguityErr := mcpPeerWorktree("dup")
		// Windows may spell the temp folder above in its short form, so only
		// the part below it is compared.
		firstMatch := filepath.Join(filepath.Base(f.Superproject), ".worktrees", "dup")
		secondMatch := filepath.Join(filepath.Base(f.Superproject), filepath.Base(f.Submodule), ".worktrees", "dup")
		if ambiguityErr == nil || !strings.Contains(ambiguityErr.Error(), firstMatch) ||
			!strings.Contains(ambiguityErr.Error(), secondMatch) {
			t.Fatal(ambiguityErr)
		}
	})
	t.Run("refused folders", func(t *testing.T) {
		subdir := filepath.Join(f.Submodule, "src")
		if err := os.MkdirAll(subdir, 0o700); err != nil {
			t.Fatal(err)
		}
		_, subdirErr := mcpPeerWorktree(subdir)
		if subdirErr == nil {
			t.Fatal("accepted checkout subdirectory")
		}
		for _, ref := range []string{
			f.Unrelated, "only-unrelated", f.UnrelatedOnly, f.Vendor,
			filepath.Join(f.Uninitialized, "module"), filepath.Join(f.Submodule, ".duolog"),
		} {
			if _, err := mcpPeerWorktree(ref); err == nil {
				t.Fatalf("accepted %s", ref)
			}
		}
	})
}

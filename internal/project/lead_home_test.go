package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestLeadHomeCachedByCwd(t *testing.T) {
	f := submodule.New(t)
	writeLeadConfig(t, f.Superproject)
	writeLeadConfig(t, f.Submodule)
	want := LeadHome(f.Linked)
	t.Setenv("PATH", t.TempDir())
	if got := LeadHome(f.Linked); got != want {
		t.Fatalf("cached lead home = %q, want %q", got, want)
	}
}

// A gate call in a project without submodules finds its lead home without
// Git, also when no earlier call of the process cached it.
func TestLeadHomeOfAPlainProjectRunsNoGit(t *testing.T) {
	main := filepath.Join(t.TempDir(), "p")
	submodule.Init(t, main)
	writeLeadConfig(t, main)
	leadHomes.Clear()
	t.Cleanup(leadHomes.Clear)
	calls := gitCalls(t)
	if got := LeadHome(main); got != filepath.Join(main, ".duolog") {
		t.Fatal(got)
	}
	if ran := calls(); len(ran) != 0 {
		t.Fatalf("lead home ran git: %q", ran)
	}
}

// A Git failure is not cached: the next call asks Git again.
func TestLeadHomeNotCachedAfterGitFailure(t *testing.T) {
	f := submodule.New(t)
	writeLeadConfig(t, f.Superproject)
	writeLeadConfig(t, f.Submodule)
	leadHomes.Clear()
	t.Cleanup(leadHomes.Clear)
	path := os.Getenv("PATH")
	failGit(t)
	same(t, LeadHome(f.Submodule), filepath.Join(f.Submodule, ".duolog"))
	t.Setenv("PATH", path)
	same(t, LeadHome(f.Submodule), filepath.Join(f.Superproject, ".duolog"))
}

// A cached lead home holds while the config files it was found by do; one
// removed or added later is seen on the next call, without Git.
func TestLeadHomeCacheRechecksConfig(t *testing.T) {
	f := submodule.New(t)
	writeLeadConfig(t, f.Superproject)
	writeLeadConfig(t, f.Submodule)
	leadHomes.Clear()
	t.Cleanup(leadHomes.Clear)
	same(t, LeadHome(f.Submodule), filepath.Join(f.Superproject, ".duolog"))
	config := filepath.Join(f.Superproject, ".duolog", "config.yaml")
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	same(t, LeadHome(f.Submodule), filepath.Join(f.Submodule, ".duolog"))
	writeLeadConfig(t, f.Superproject)
	same(t, LeadHome(f.Submodule), filepath.Join(f.Superproject, ".duolog"))
}

func TestCapGroupFollowsTopCheckout(t *testing.T) {
	f := submodule.New(t)
	home := filepath.Join(f.Superproject, ".duolog")
	for _, tc := range []struct{ work, want string }{
		{f.Superproject, f.Superproject},
		{f.SuperLinked, f.SuperLinked},
		{f.Submodule, f.Superproject},
		{f.Linked, f.Superproject},
		{f.Nested, f.Superproject},
		{f.SuperLinkedSubmodule, f.SuperLinked},
		{f.Unrelated, f.Unrelated},
	} {
		same(t, capGroup(t, tc.work, home), tc.want)
	}
	if got := capGroup(t, filepath.Join(t.TempDir(), "missing"), home); got != "" {
		t.Fatalf("missing root has group %q", got)
	}
}

// The walk stops at the lead home's project: a lead home in a submodule of an
// unconfigured project groups the submodule and its own submodules there.
func TestCapGroupStopsAtLeadHomeProject(t *testing.T) {
	f := submodule.New(t)
	home := filepath.Join(f.Submodule, ".duolog")
	for _, work := range []string{f.Submodule, f.Nested} {
		same(t, capGroup(t, work, home), f.Submodule)
	}
	failGit(t)
	if group, err := CapGroup(f.Nested, home); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("a Git failure gave group %q, %v", group, err)
	}
}

func capGroup(t *testing.T, work, home string) string {
	t.Helper()
	group, err := CapGroup(work, home)
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func TestLeadHomeRelations(t *testing.T) {
	f := submodule.New(t)
	for _, root := range []string{
		f.Superproject, f.Submodule, f.Nested, f.Unrelated,
		f.SuperLinked, f.SuperLinkedSubmodule,
	} {
		writeLeadConfig(t, root)
	}
	t.Run("configured relations", func(t *testing.T) {
		same(t, LeadHome(f.Submodule), filepath.Join(f.Superproject, ".duolog"))
		same(t, LeadHome(f.Linked), LeadHome(f.Superproject))
		same(t, LeadHome(f.Nested), LeadHome(f.Superproject))
		same(t, LeadHome(f.SuperLinkedSubmodule), LeadHome(f.Superproject))
		same(t, LeadHome(f.SuperLinked), LeadHome(f.Superproject))
		same(t, StateRoot(f.Linked), f.Submodule)
		same(t, StateRoot(f.Nested), f.Nested)
		same(t, StateRoot(f.SuperLinkedSubmodule), f.SuperLinkedSubmodule)
		same(t, Superproject(f.SuperLinkedSubmodule), f.SuperLinked)
		if Superproject(f.Unrelated) != "" {
			t.Fatal("plain nesting linked leads")
		}
		same(t, LeadHome(f.Unrelated), filepath.Join(f.Unrelated, ".duolog"))
	})
	t.Run("config edges", func(t *testing.T) {
		testLeadHomeConfigEdges(t, f)
	})
	t.Run("hostile git environment", func(t *testing.T) {
		t.Setenv("GIT_DIR", filepath.Join(f.Unrelated, ".git"))
		t.Setenv("GIT_WORK_TREE", f.Unrelated)
		for _, root := range []string{f.Submodule, f.Linked, f.Nested, f.SuperLinkedSubmodule} {
			same(t, LeadHome(root), filepath.Join(f.Superproject, ".duolog"))
		}
		same(t, StateRoot(f.Linked), f.Submodule)
		if Superproject(f.Unrelated) != "" {
			t.Fatal("hostile environment linked an unrelated repository")
		}
		// The reusable fixture must also ignore an inherited Git repository.
		other := submodule.New(t)
		same(t, Superproject(other.Linked), other.Superproject)
	})
	t.Run("symlink alias", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(f.Superproject, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		got := LeadHome(filepath.Join(alias, "module", "nested"))
		want := filepath.Join(alias, ".duolog")
		if got != want {
			t.Fatalf("lead home = %q, want alias %q", got, want)
		}
		same(t, got, filepath.Join(f.Superproject, ".duolog"))
	})
	t.Run("non-git directories", testLeadHomeOutsideGit)
}

func testLeadHomeConfigEdges(t *testing.T, f submodule.Fixture) {
	t.Helper()
	cases := []struct {
		name, root string
		from, want []string
	}{
		{
			name: "submodule", root: f.Submodule,
			from: []string{f.Submodule, f.Linked, f.Nested, f.SuperLinkedSubmodule},
			want: []string{f.Submodule, f.Submodule, f.Nested, f.Superproject},
		},
		{
			name: "superproject", root: f.Superproject,
			from: []string{f.Submodule, f.Linked, f.Nested, f.SuperLinkedSubmodule, f.SuperLinked},
			want: []string{f.Submodule, f.Submodule, f.Submodule, f.SuperLinkedSubmodule, f.Superproject},
		},
		{
			name: "nested", root: f.Nested,
			from: []string{f.Nested}, want: []string{f.Nested},
		},
	}
	for _, tc := range cases {
		for _, kind := range []string{"missing", "directory"} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				leadHomes.Clear()
				t.Cleanup(leadHomes.Clear)
				config := filepath.Join(tc.root, ".duolog", "config.yaml")
				if err := os.Remove(config); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if kind == "directory" {
						if err := os.Remove(config); err != nil {
							t.Fatal(err)
						}
					}
					writeLeadConfig(t, tc.root)
				})
				if kind == "directory" {
					if err := os.Mkdir(config, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				for i, root := range tc.from {
					same(t, LeadHome(root), filepath.Join(tc.want[i], ".duolog"))
				}
			})
		}
	}
}

func testLeadHomeOutsideGit(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, ".duolog")
	if got := LeadHome(root); got != want {
		t.Fatalf("lead home = %q, want %q", got, want)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("resolver created state directory: %v", err)
	}
	writeLeadConfig(t, root)
	same(t, LeadHome(root), want)
}

func writeLeadConfig(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".duolog")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Resolution checks the file type only; parsing belongs to the loader.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("invalid: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

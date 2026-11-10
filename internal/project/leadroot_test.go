package project

import (
	"os"
	"path/filepath"
	"testing"
)

// A command run inside the worktree it starts a worker in has that worktree
// as its current checkout, and a folder cannot give trust to itself: the
// source is the project's main checkout. Mutation killed: return current
// always, the bug, which left every `cd .worktrees/x && duolog delegate`
// without the trust of the lead's checkout.
func TestLeadRootOfTheTargetItselfIsTheMainCheckout(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	mainCheckout, worktree, other := filepath.Join(base, "main"), filepath.Join(base, "wt"), filepath.Join(base, "other")
	for _, dir := range []string{mainCheckout, worktree, other} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(worktree, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, current, target, want string
	}{
		{"inside the target", worktree, worktree, mainCheckout},
		{"inside the target, by another spelling", link, worktree, mainCheckout},
		{"in the main checkout", mainCheckout, worktree, mainCheckout},
		{"in another worktree", other, worktree, other},
		{"a target that is gone", worktree, filepath.Join(base, "gone"), worktree},
	}
	for _, tc := range cases {
		if got := leadRoot(tc.current, mainCheckout, tc.target); got != tc.want {
			t.Errorf("%s: leadRoot = %q, want %q", tc.name, got, tc.want)
		}
	}
}

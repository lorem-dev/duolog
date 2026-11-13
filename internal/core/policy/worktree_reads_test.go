package policy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// A worker in a linked worktree gets the project's main checkout as a Read
// entry (the gate's mainCheckoutReads), so it reads the plans and notes that
// Git ignores and that live only there. The entry opens plain reads alone:
// the state rule, every read deny and the folder check of a search stay.
func TestMainCheckoutReadEntryFromAWorktree(t *testing.T) {
	p := policy(t)
	main := pathutil.Resolved(t.TempDir())
	work := filepath.Join(main, ".worktrees", "w")
	for _, file := range []string{".superpowers/plans/a.md", "docs/b.md", ".env", ".duolog/bus.ndjson", "secrets/prod.key", ".worktrees/w/docs/c.md"} {
		path := filepath.Join(main, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := filepath.ToSlash(main)
	for _, tc := range []struct{ command, want string }{
		{"sed -n 1,20p " + m + "/.superpowers/plans/a.md", Allow},
		{"cat " + m + "/docs/b.md", Allow},
		{"rg -n x " + m + "/.superpowers/plans", Allow},
		{"cat " + m + "/.env", Deny},
		{"cat " + m + "/.duolog/bus.ndjson", Deny},
		{"cat " + m + "/secrets/prod.key", Deny},
		// A search of the whole checkout reaches hidden names: it asks.
		{"rg -n x " + m, Ask},
	} {
		for _, role := range []struct {
			name string
			lead LeadRules
		}{{"implement", LeadRules{}}, {"review_final", LeadRules{RoleSandbox: "read-only"}}} {
			a := Action{Kind: "exec", Target: tc.command, Role: role.name, Work: work, Cwd: work, State: main, Reads: []string{main}, WideRead: wideSkillRoot}
			if d := Evaluate(p, role.lead, mode.Full, a); d.Kind != tc.want {
				t.Errorf("%s %q: %s (%s, %s), want %s", role.name, tc.command, d.Kind, d.Rule, d.Reason, tc.want)
			}
		}
	}
	// The Read tool takes the same entry.
	read := func(name string) Decision {
		target := RelativeTarget(work, work, filepath.Join(main, filepath.FromSlash(name)))
		return Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "read", Target: target, Role: "implement", Work: work, Reads: []string{main}, WideRead: wideSkillRoot})
	}
	if d := read(".superpowers/plans/a.md"); d.Kind != Allow {
		t.Errorf("Read of a plan: %+v", d)
	}
	for _, name := range []string{".env", ".duolog/bus.ndjson", "secrets/prod.key"} {
		if d := read(name); d.Kind != Deny {
			t.Errorf("Read of %s: %+v, want deny", name, d)
		}
	}
}

// sed -n with a line range prints its input in a pipe as it prints a file.
func TestSedPrintRangeReadsItsInput(t *testing.T) {
	p := policy(t)
	for _, command := range []string{`nl -ba docs/a.md | sed -n '1,150p'`, `cat docs/a.md | sed -n 3p`, `sed -n '1,3p' docs/a.md`} {
		d := Evaluate(p, LeadRules{RoleSandbox: "read-only"}, mode.Full, Action{Kind: "exec", Target: command, Role: "review_final"})
		if d.Kind != Allow {
			t.Errorf("%q: %s (%s, %s), want allow", command, d.Kind, d.Rule, d.Reason)
		}
	}
	for _, command := range []string{`cat docs/a.md | sed -n 1,3w out.txt`, `cat docs/a.md | sed -n -i 1p`, `cat docs/a.md | sed -n 1e`} {
		if d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement"}); d.Kind == Allow {
			t.Errorf("%q: allowed", command)
		}
	}
}

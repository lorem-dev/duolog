package policy

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// patternGlobCase evaluates command in a new work root, with /elsewhere
// standing for a path outside it.
func patternGlobCase(t *testing.T, p config.Policy, command string, m mode.Mode) (Action, Decision) {
	t.Helper()
	root := t.TempDir()
	command = strings.ReplaceAll(command, "/elsewhere", absoluteTestPath(root, "/elsewhere"))
	a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
	return a, Evaluate(p, LeadRules{}, m, a)
}

// The shell expands an unquoted glob before the program runs, so a glob where
// the pattern stands can become several words: the first is the pattern and
// the rest are files that the program reads. Each such word that can leave
// the work root needs the person, in every mode.
func TestPatternGlobOutsideNeedsThePerson(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		`grep /elsewhere/pa* f`,
		`grep ../pa* f`,
		`grep -n /elsewhere/pa? f`,
		`grep /elsewhere/[p]a f`,
		`grep -- /elsewhere/pa* f`,
		`rg /elsewhere/pa* f`,
		`rg -n ../pa* f`,
		`rg -g /elsewhere/* x`,
		`git log -G /elsewhere/pa* x`,
		`git log -S /elsewhere/pa* x`,
		`git log --grep /elsewhere/pa* x`,
		`git log --author /elsewhere/pa* x`,
		`git log --committer /elsewhere/pa* x`,
		`git diff -G /elsewhere/pa* x`,
		`git show -S /elsewhere/pa* x`,
		`git grep /elsewhere/pa* f`,
		`git grep -n ../pa* f`,
		`jq /elsewhere/pa* f`,
		`jq ../l* f`,
		`sed /elsewhere/* f`,
		`sed -n /elsewhere/* f`,
		`awk /elsewhere/* f`,
		`cd sub && grep ../../pa* f`,
		`cat f | grep /elsewhere/pa*`,
		`grep .?/pa* f`,
		`grep /elsewhere/x f -*`,
		`grep -C /elsewhere/pa* f`,
		`LC_ALL=C grep /elsewhere/pa* f`,
		`grep /elsewhere/pa* f 2>/dev/null`,
	} {
		for _, m := range []mode.Mode{mode.Edits, mode.Full} {
			if a, d := patternGlobCase(t, p, command, m); !PersonOnly(d) && d.Kind != Deny {
				t.Errorf("%s: %q: %+v, outside=%v; want a person-only ask", m, command, d, ExecOutside(a))
			}
		}
	}
}

// A glob through a link that leads out expands onto files outside.
func TestPatternGlobThroughALinkNeedsThePerson(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs a privilege on Windows")
	}
	p := policy(t)
	for _, command := range []string{`grep lnk/pa* f`, `rg lnk/pa* f`, `git log -G lnk/pa* x`, `jq lnk/pa* f`} {
		root := t.TempDir()
		outsideDir := t.TempDir()
		if err := os.Symlink(outsideDir, filepath.Join(root, "lnk")); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"pa", "pb"} {
			if err := os.WriteFile(filepath.Join(outsideDir, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		a := Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root}
		if d := Evaluate(p, LeadRules{}, mode.Edits, a); !PersonOnly(d) && d.Kind != Deny {
			t.Errorf("%q: %+v, outside=%v; want a person-only ask", command, d, ExecOutside(a))
		}
	}
}

// A quoted pattern is one word that the shell does not expand, and a glob
// that stays inside the work root reads only there: neither needs the person.
func TestPatternGlobThatStaysKeepsItsDecision(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		command string
		want    string
	}{
		{`grep '/elsewhere/pa*' f`, Ask},
		{`grep "/elsewhere/pa*" f`, Ask},
		{`grep \/elsewhere/pa\* f`, Ask},
		{`grep -n '../pa*' f`, Ask},
		{`rg '/elsewhere/pa*' f`, Ask},
		{`git log -G '/elsewhere/pa*' x`, Ask},
		{`git log -S "/elsewhere/pa*" x`, Ask},
		{`git log --grep '/elsewhere/pa*' x`, Ask},
		{`grep pa* f`, Ask},
		{`grep sub/pa? f`, Ask},
		{`rg pa* f`, Ask},
		{`git log -G pa* x`, Ask},
		{`jq .a* f`, Ask},
		{`grep -c '\bLoad\(' internal/*_test.go`, Ask},
		{`find . -name *.go`, Allow},
		{`grep --include=*.go x f`, Allow},
		{`grep x f`, Allow},
		{`grep '.duolog/*' f`, Ask},
	} {
		if a, d := patternGlobCase(t, p, tc.command, mode.Edits); d.Kind != tc.want || PersonOnly(d) {
			t.Errorf("%q: %+v, outside=%v; want %s and no person-only ask", tc.command, d, ExecOutside(a), tc.want)
		}
	}
}

// The shell drops the quotes of a program name before it runs, so a quoted
// program takes the rule of the program it names.
func TestPatternGlobBehindAQuotedProgram(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ glob, want string }{{`.e*`, Ask}, {`.d*/*`, Deny}} {
		for _, form := range []string{
			`'grep' -v %s f`, `"grep" %s f`, `g"r"ep %s f`, `'grep' -- %s f`, `'rg' -v %s f`,
			`'jq' %s f`, `'git' log -G %s x`, `cat f | 'grep' -v %s`,
		} {
			command := strings.ReplaceAll(form, "%s", tc.glob)
			for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
				if _, d := patternGlobCase(t, p, command, m); d.Kind != tc.want {
					t.Errorf("%s: %q: %+v, want %s", m, command, d, tc.want)
				}
			}
		}
	}
}

// With on_unmatched: deny and no rule for the command, the glob's ask must not
// take the place of the deny that the plain command gets.
func TestPatternGlobKeepsOnUnmatchedDeny(t *testing.T) {
	p := policy(t)
	p.OnUnmatched = Deny
	p.Rules = slices.DeleteFunc(slices.Clone(p.Rules), func(r config.Rule) bool { return r.Action == "exec" && r.Decision == Allow })
	for _, command := range []string{`grep pa f`, `grep pa* f`, `grep .e* f`, `git log -G pa* x`, `jq .a* f`, `rg pa* f`} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			if _, d := patternGlobCase(t, p, command, m); d.Kind != Deny {
				t.Errorf("%s: %q: %+v, want a deny", m, command, d)
			}
		}
	}
}

// docs/permissions.md: a shell glob in a path asks, because it can expand onto
// denied files. Where a read command takes a pattern the shell can make a path
// of the glob too, so it gets the decision that cat gets for that glob, whether
// or not a denied file exists now: grep .e* f reads .env when .editorconfig
// comes first.
func TestPatternGlobGetsTheDecisionOfAPathGlob(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ glob, want string }{
		{`.e*`, Ask},
		{`.en?`, Ask},
		{`*.pem`, Ask},
		{`secrets/*`, Ask},
		{`pa*`, Ask},
		{`.duolog/*`, Deny},
		{`.d*/*`, Deny},
		{`sub/.duolog/*`, Deny},
	} {
		for _, form := range []string{
			`grep %s f`, `grep -n %s f`, `grep -- %s f`, `grep -e %s f`, `rg %s f`, `rg -n %s f`, `rg -e %s f`,
			`git log -G %s x`, `git log -S %s x`, `git log --grep %s x`, `git diff -G %s x`, `git show -S %s x`, `jq %s f`, `jq -r %s f`, `jq --arg a %s . f`,
			`! grep %s f`, `cat f | grep %s`,
		} {
			command := strings.ReplaceAll(form, "%s", tc.glob)
			for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
				_, path := patternGlobCase(t, p, "cat "+tc.glob, m)
				_, d := patternGlobCase(t, p, command, m)
				if path.Kind != tc.want {
					t.Fatalf("%s: cat %s: %+v, want %s", m, tc.glob, path, tc.want)
				}
				if d.Kind != path.Kind {
					t.Errorf("%s: %q: %+v, want the decision of cat %s: %+v", m, command, d, tc.glob, path)
				}
			}
		}
		// These are no read commands, so no read rule allows them.
		for _, form := range []string{`egrep %s f`, `fgrep %s f`, `git grep %s f`, `if grep %s f; then ls; fi`} {
			command := strings.ReplaceAll(form, "%s", tc.glob)
			for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
				if _, d := patternGlobCase(t, p, command, m); d.Kind == Allow {
					t.Errorf("%s: %q: %+v", m, command, d)
				}
			}
		}
	}
}

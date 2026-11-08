package policy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// treeCase evaluates command in a new work root that holds files, each an
// empty file at a path relative to the root.
func treeCase(t *testing.T, p config.Policy, files []string, command string, m mode.Mode) Decision {
	t.Helper()
	root := t.TempDir()
	for _, name := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command = strings.ReplaceAll(command, "/elsewhere", absoluteTestPath(root, "/elsewhere"))
	return Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root})
}

// A glob in the value of an option that takes one stays that option's value:
// the shell can only turn it into the same option with a file's name in it.
func TestOptionGlobValueIsAPlainRead(t *testing.T) {
	p := policy(t)
	tree := []string{"cmd/a.go", "internal/b.go", "internal/notes.md", "f"}
	for _, command := range []string{
		`grep -rn "TaskLine(" cmd internal --include=*.go`,
		`grep -rn x cmd internal --exclude=*.md`,
		`grep -rn x cmd internal --exclude-dir=.cache`,
		`grep -rn x cmd --include=*.go --exclude=*_test.go`,
		`grep -n x f --include=*.go`,
		`rg -g*.go x`,
		`rg --glob=*.go x`,
		`rg --iglob=*.go x`,
		`rg -g *.go x`,
		`rg --glob *.go x`,
		`rg . -g *.go`,
		`grep --include *.go x f`,
	} {
		for _, m := range allModes {
			if d := treeCase(t, p, tree, command, m); d.Kind != Allow {
				t.Errorf("%s: %q: %+v, want an allow", m, command, d)
			}
		}
	}
}

// The shell keeps the literal start of a word, so --include=*.go can only
// become --include=<name>, and what that name selects is a part of what the
// glob selects. A read of a denied file is decided as it was without the file
// named like the option.
func TestFileNamedLikeTheOptionChangesNoDeniedRead(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		name    string
		files   []string
		command string
		allowed bool
	}{
		{"denied operand", []string{"--include=a.go", "a.go"}, `grep x .env --include=*.go`, false},
		{"denied file the glob selects", []string{"--include=.env", ".env"}, `grep -rn S . --include=.e*`, false},
		{"denied file matching the glob", []string{"--include=x.go", ".env.go"}, `grep -rn S . --include=*.go`, false},
		{"denied folder below a root", []string{"--include=x.go", "cmd/secrets/a.go"}, `grep -rn S cmd --include=*.go`, false},
		{"exclude does not protect", []string{"--exclude=.example", "cmd/.env"}, `grep -rn S cmd --exclude=.e*`, false},
		{"state glob in a root", []string{"--include=a.go", ".duolog/bus.ndjson"}, `grep x .d*/* --include=*.go`, false},
		{"no denied file", []string{"--include=x.go", "cmd/a.go"}, `grep -rn S cmd --include=*.go`, true},
		{"expansion narrows", []string{"--include=x.go", "x.go", "y.go"}, `grep -rn S . --include=*.go`, true},
	} {
		for _, m := range allModes {
			d := treeCase(t, p, tc.files, tc.command, m)
			if tc.allowed && d.Kind != Allow || !tc.allowed && d.Kind == Allow {
				t.Errorf("%s: %s: %q: %+v, allowed=%v", m, tc.name, tc.command, d, tc.allowed)
			}
		}
	}
	for _, m := range allModes {
		if d := treeCase(t, p, []string{"--include=a.go", ".env"}, `grep x .env --include=*.go`, m); d.Kind != Deny {
			t.Errorf("%s: a denied operand beside an expansion: %+v, want a deny", m, d)
		}
	}
}

// A separate value can become several words, and each after the first is a
// pattern or a file: a word that was a pattern then names a file to read.
func TestSeparateOptionGlobNeedsOneWord(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		files   []string
		command string
	}{
		{[]string{"a.go", "b.go"}, `rg -g *.go x`},
		{[]string{"a.go", "b.go"}, `rg -g *.go .env`},
		{[]string{"a.go", "b.go"}, `rg --glob *.go x`},
		{[]string{"a.go", "b.go"}, `rg . -g *.go`},
		{[]string{"a.go", "b.go"}, `grep --include *.go x f`},
		{[]string{"a.pem"}, `rg -g *.pem x`},
		{nil, `rg -g .e* x`},
		{nil, `rg -g*.pem x`},
		{nil, `rg -g{a,.env} x`},
		{nil, `grep --include {a,b} x f`},
	} {
		if d := treeCase(t, p, tc.files, tc.command, mode.Edits); d.Kind == Allow {
			t.Errorf("%q in %v: %+v, want no allow", tc.command, tc.files, d)
		}
	}
}

// Only the options the gate knows to take a glob pass; a recursive search
// without one still asks, and so does a tree it cannot prove.
func TestOtherGlobsKeepTheirAsk(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		`grep -rn x cmd`,
		`grep -rn x cmd --label=*.go`,
		`grep -n x f --file=*.go`,
		`grep -n x -f*.go f`,
		`grep -n x f *.go`,
		`grep -n x f -*`,
	} {
		if d := treeCase(t, p, []string{"cmd/a.go", "f"}, command, mode.Edits); d.Kind == Allow {
			t.Errorf("%q: %+v, want no allow", command, d)
		}
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "cmd", "a.go")); err != nil {
		t.Skip("cannot create a symlink here")
	}
	a := Action{Kind: "exec", Target: `grep -rn x cmd --include=*.go`, Role: "implement", Work: root, Cwd: root}
	if d := Evaluate(p, LeadRules{}, mode.Edits, a); d.Kind == Allow {
		t.Errorf("through a symlink: %+v, want no allow", d)
	}
}

// A path that the shell builds from a variable can name anything, so only the
// person can allow it.
func TestPathBuiltByExpansionNeedsThePerson(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		`cat $HOME/.ssh/id_test`,
		`cat "$HOME/.ssh/id_test"`,
		`cat ${HOME}/.ssh/id_test`,
		`cat $PWD/../outside/secret`,
		// treeCase puts the drive before /elsewhere on Windows, so $IFS would
		// run on into a variable named IFSC; the braces keep the name whole.
		`cat${IFS}/elsewhere/secret`,
		`cat$IFS/outside/secret`,
		`cat x$IFS.env`,
		`head -1 $X/y`,
		`for f in $(ls); do cat $f/x; done`,
		`X=$HOME; cat $X/y`,
		`for X in a; do read X; cat $X/y; done`,
	} {
		for _, m := range allModes {
			if d := treeCase(t, p, nil, command, m); !PersonOnly(d) && d.Kind != Deny {
				t.Errorf("%s: %q: %+v, want the person or a deny", m, command, d)
			}
		}
	}
	// On Windows a backslash can separate folders whatever the shell, so \$HOME/x
	// may be rooted on the current drive and needs the person there.
	if d := treeCase(t, p, nil, `cat \$HOME/x`, mode.Edits); PersonOnly(d) != (runtime.GOOS == "windows") {
		t.Errorf("an escaped $ on %s: %+v", runtime.GOOS, d)
	}
	for _, command := range []string{
		`echo $HOME/x`,
		`echo "$DUOLOG_PEER"`,
		`cat '$HOME/x'`,
		"cat > notes.md <<'EOF'\n$HOME/x\nEOF",
		`go test -run $X ./...`,
		`for f in a b; do cp $f bak/$f; done`,
		`for d in a b; do cat $d/x; done`,
		`d=sub; cat $d/x`,
	} {
		if d := treeCase(t, p, nil, command, mode.Edits); PersonOnly(d) {
			t.Errorf("%q: %+v, want no person-only ask", command, d)
		}
	}
}

// A form that reaches duolog state is denied as its plain form is.
func TestFormsThatReachStateAreDenied(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		`LC_ALL=C cat .duolog/bus.ndjson`,
		`grep S <.du*/bus.ndjson`,
		`cat < .en?`,
		`env grep .d*/* f`,
		`/usr/bin/grep .d*/* f`,
		`xargs grep .d*/* f`,
		`grep -v .d*/* f --mmap`,
		`rg -v .d*/* f --pcre2`,
		`'env' grep -v .d*/* f`,
		`env cat .duolog/bus.ndjson`,
		`env -i git log -p -- .duolog/bus.ndjson`,
		`timeout 5 cat .duolog/bus.ndjson`,
	} {
		for _, m := range allModes {
			if d := treeCase(t, p, nil, command, m); d.Kind != Deny {
				t.Errorf("%s: %q: %+v, want a deny", m, command, d)
			}
		}
	}
	for _, command := range []string{
		`LC_ALL=C cat notes.md`,
		`LC_ALL=C cat .duolog/brief.md`,
		`env grep x f`,
		`/usr/bin/grep x f`,
		`cat < src/*.go`,
		`grep -v src/* f --mmap`,
	} {
		for _, m := range []mode.Mode{mode.Ask, mode.Edits} {
			if d := treeCase(t, p, nil, command, m); d.Kind == Deny {
				t.Errorf("%s: %q: %+v, want no deny", m, command, d)
			}
		}
	}
}

// A jq module search path names a folder jq reads code and data from.
func TestJqSearchOutsideNeedsThePerson(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		`jq -n 'import "data" as $d {search:"/elsewhere"}; $d'`,
		`jq -n 'import "data" as $d {"search":"/elsewhere"}; $d'`,
		`jq -n 'import "data" as $d {search:["./lib","/elsewhere"]}; $d'`,
		`jq -n 'include "data" {search:"../outside"}; .'`,
		`jq -n 'import "data" as $d {search:$x}; $d'`,
		`jq -n 'import "data" as $d {search:"/elsewhere"}; $d'`,
	} {
		for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
			if d := treeCase(t, p, nil, command, m); !PersonOnly(d) {
				t.Errorf("%s: %q: %+v, want the person", m, command, d)
			}
		}
	}
	for _, command := range []string{
		`jq -n 'import "data" as $d {search:"./lib"}; $d'`,
		`jq .search f`,
		`jq '.a | {search:"/x"}' f`,
	} {
		if d := treeCase(t, p, nil, command, mode.Edits); PersonOnly(d) {
			t.Errorf("%q: %+v, want no person-only ask", command, d)
		}
	}
}

// Git's * and ? cross a slash, so these are aimed at a denied name.
func TestPathspecGlobAcrossASlashIsDenied(t *testing.T) {
	p := policy(t)
	for _, glob := range []string{`secrets*`, `sec*x`, `sub?.env`, `*?.env`, `.duolog?bus*`} {
		for _, form := range []string{`git log -p -- '%s'`, `git diff -- '%s'`} {
			command := strings.ReplaceAll(form, "%s", glob)
			for _, m := range allModes {
				if d := treeCase(t, p, nil, command, m); d.Kind != Deny {
					t.Errorf("%s: %q: %+v, want a deny", m, command, d)
				}
			}
		}
	}
}

// Too little text to say what it means alone: the tree says whether a denied
// file is there, and without one the lead decides, as for any ordinary glob.
func TestShortPathspecGlobFollowsTheTree(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		glob  string
		files []string
		want  string
	}{
		{`.e*`, []string{".env"}, Deny},
		{`.e*`, []string{".editorconfig"}, Ask},
		{`.e*`, nil, Ask},
		{`se*`, []string{"secrets/x"}, Deny},
		{`se*`, []string{"server/x.go"}, Ask},
		{`se*`, nil, Ask},
		{`a*m`, []string{"a.pem"}, Deny},
		{`a*m`, []string{"sub/a.pem"}, Ask},
		{`a*m`, []string{"alarm"}, Ask},
		{`a*m`, nil, Ask},
		{`*`, []string{".env"}, Ask},
		{`*.go`, []string{"secrets/x.go"}, Ask},
		{`src/*`, []string{"src/.env"}, Ask},
		{`s*`, []string{"secrets/x"}, Ask},
	} {
		command := `git log -p -- '` + tc.glob + `'`
		for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
			if d := treeCase(t, p, tc.files, command, m); d.Kind != tc.want || PersonOnly(d) {
				t.Errorf("%s: %q in %v: %+v, want %s", m, command, tc.files, d, tc.want)
			}
		}
	}
}

// A dot and a letter say an extension only while the tree has nothing else to
// show for the glob.
func TestExtensionGlobFollowsTheTree(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{[]string{"main.py"}, Ask},
		{[]string{"main.py", "main.pem"}, Deny},
		{nil, Deny},
		{[]string{"other.go"}, Deny},
	} {
		for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
			if d := treeCase(t, p, tc.files, `git log -p -- 'main.p*'`, m); d.Kind != tc.want || PersonOnly(d) {
				t.Errorf("%s: in %v: %+v, want %s", m, tc.files, d, tc.want)
			}
		}
	}
}

// A word that starts with a glob can become an option too, as one that starts
// with - can.
func TestGitOptionThroughAGlob(t *testing.T) {
	p := policy(t)
	for _, command := range []string{`git log [-]*`, `git log ?L*`, `git diff [-]*`, `git log [!a]*`, `git log *L*`, `git log *`} {
		for _, role := range []string{"implement", "review"} {
			for _, m := range allModes {
				root := t.TempDir()
				d := Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: role, Work: root, Cwd: root})
				if want := m == mode.Plan || role == "review"; want && d.Kind != Deny || d.Kind == Allow {
					t.Errorf("%s %s: %q: %+v", role, m, command, d)
				}
			}
		}
	}
	for _, command := range []string{`git log *.go`, `git diff src/*`, `git log [ab]*`, `git diff -- [-]*`, `git log -- ?L*`} {
		for _, m := range allModes {
			if d := treeCase(t, p, nil, command, m); d.Kind != Ask {
				t.Errorf("%s: %q: %+v, want an ask", m, command, d)
			}
		}
	}
}

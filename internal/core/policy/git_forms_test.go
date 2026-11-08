package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// git log -L prints the lines of the file it names, and a pathspec may carry
// magic in front of the path: each reads the file as git log -p -- file does,
// and a quoted pathspec glob is expanded by Git itself.
func TestGitFormsOfADeniedPathAreDenied(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		`git log -p -- .env`,
		`git log -L1,1:.env`,
		`git log -L 1,1:.env`,
		`git log -L1,1:.env HEAD`,
		`git log -L:f:.env`,
		`git log -L :f:.env`,
		`git log -L/a:b/,+1:.env`,
		`git log -L1,1:secrets/x`,
		`git log -L1,1:a.pem`,
		`git log -L1,1:.duolog/bus.ndjson`,
		`git log -L 1,1:a:b/.env`,
		`git log -p -- ':(top).env'`,
		`git log -p -- ':(icase).ENV'`,
		`git log -p -- ':(top,icase).ENV'`,
		`git log -p -- ':(glob)**/.env'`,
		`git log -p -- ':/.env'`,
		`git log -p ':(top).env'`,
		`git show ':(top).env'`,
		`git show :0:.env`,
		`git log -p -- '.en?'`,
		`git log -p -- '.en*'`,
		`git log -p -- '*.pem'`,
		`git diff -- '.en?'`,
		`git log -p -- .en?`,
		`git log -L ^:SE:.env`,
		`git log -L '^:SE:.env'`,
		`git log -L^:SE:.env`,
		`git log -L ^:SE:secrets/x`,
		`git log -L ^:SE:a.pem`,
		`git log -L ^:SE:.duolog/bus.ndjson`,
		`git log -p -- 'sec*/x'`,
		`git log -p -- '[s]ecrets/x'`,
		`git log -p -- 'a.p?m'`,
		`git log -p -- 'secre?s/x'`,
		`git log -p -- 'a.p*'`,
		`git log -p -- '.duol*'`,
		`git log -p -- '.d*'`,
		`git log -p -- '.duolog*'`,
		`git log -p -- '[.]env'`,
		`git log -p -- '[s][e][c]*/x'`,
		`git log -p -- 'secrets/*'`,
		`git log -p -- 'sub/.en?'`,
		`git log -p -- '*env'`,
		`git log -p -- '?env'`,
		`git log -p -- '\.en*'`,
		`git diff -- ':(glob)sec*/x'`,
	} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			if _, d := patternGlobCase(t, p, command, m); d.Kind != Deny {
				t.Errorf("%s: %q: %+v, want a deny", m, command, d)
			}
		}
	}
}

// What names no denied file keeps its decision. An excluded path is not read.
func TestGitFormsOfAnAllowedPathKeepTheirDecision(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, want string }{
		{`git log -L1,1:a.go`, Allow},
		{`git log -L 1,1:a.go`, Allow},
		{`git log -L:f:a.go`, Allow},
		{`git log -p -- ':(top)a.go'`, Allow},
		{`git log -p -- ':/a.go'`, Allow},
		{`git log -p -- . ':!.env'`, Allow},
		{`git log -p -- . ':^.env'`, Allow},
		{`git log -p -- . ':(exclude).env'`, Allow},
		{`git log -p -- ':(icase)a.go'`, Ask},
		{`git log -p -- '*.go'`, Ask},
		{`git log -p -- f`, Allow},
		{`git log -p -- src/a.go`, Allow},
		{`git log -L1,3:src/a.go`, Allow},
		{`git log -L :A:src/a.go`, Allow},
		{`git log -L ^:A:src/a.go`, Allow},
		{`git log -L^:A:src/a.go`, Allow},
		{`git log -L '^:A:src/a.go'`, Allow},
		{`git log -L '/^func A/,+1:src/a.go'`, Allow},
		{`git log -L '^/func A/,+1:src/a.go'`, Allow},
		{`git log -L:A:src/a.go`, Allow},
	} {
		if _, d := patternGlobCase(t, p, tc.command, mode.Edits); d.Kind != tc.want || PersonOnly(d) {
			t.Errorf("%q: %+v, want %s and no person-only ask", tc.command, d, tc.want)
		}
	}
}

// Windows refuses a colon in a path as a stream name. What Git reads of an
// attached -L value is the file after the range, so that is what the rule sees:
// the range is no stream, and a stream or a denied name in the file still is.
func TestGitAttachedLineRangeUnderWindowsNames(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, want string }{
		{`git log -L1,3:src/a.go`, Allow},
		{`git log -L:A:src/a.go`, Allow},
		{`git log -L^:A:src/a.go`, Allow},
		{`git log -L1,3:src/.env`, Deny},
		{`git log -L1,1:.env`, Deny},
		{`git log -L:A:.env`, Deny},
		{`git log -L:A:src/secrets/x`, Deny},
		{`git log -L1,1:src/a.go:evil`, Deny},
		{`git log -L:A:src/a.go:evil`, Deny},
		{`git log -L1,1:.env::$DATA`, Deny},
		// A value that is no plain range stays judged whole.
		{`git log -Lsecrets/x/:y`, Deny},
	} {
		root := t.TempDir()
		a := Action{Kind: "exec", Target: tc.command, Role: "implement", Work: root, Cwd: root, Shell: "windows"}
		if d := Evaluate(p, LeadRules{}, mode.Edits, a); d.Kind != tc.want || PersonOnly(d) {
			t.Errorf("%q: %+v, want %s and no person-only ask", tc.command, d, tc.want)
		}
	}
}

// A backslash may be an escape in the shell that runs the line, so a Windows
// path written with backslashes is syntax the gate cannot read for sure: it
// goes to the lead, on every host. Forward slashes read as plain words.
func TestWindowsPathWithBackslashesAsks(t *testing.T) {
	p := policy(t)
	p.Rules = append(p.Rules, config.Rule{Action: "exec", Match: "task-check *", Decision: Allow, Roles: []string{"implement"}})
	root := t.TempDir()
	for _, tc := range []struct{ command, want string }{
		{`task-check sub\x`, Ask},
		{`task-check sub/x`, Allow},
	} {
		a := Action{Kind: "exec", Target: tc.command, Role: "implement", Work: root, Cwd: root, Shell: "windows"}
		if d := Evaluate(p, LeadRules{}, mode.Edits, a); d.Kind != tc.want {
			t.Errorf("%q: %+v, want %s", tc.command, d, tc.want)
		}
	}
}

// A pathspec glob that holds too little of a denied name to say it means that
// name goes to the lead, so the lead can still allow an ordinary read: a dot
// alone is no extension, and a letter or two is no name.
func TestGitGlobWithoutTheDeniedTextAsks(t *testing.T) {
	p := policy(t)
	for _, glob := range []string{
		`main.*`, `src/foo.*`, `README.*`, `a.*`, `*.*`, `src/*.*`, `*.???`,
		`[a-z]*/a.go`, `*s/*.go`, `internal/*s/*.go`, `*t*/a.go`, `*e*/*.go`,
		`s*/x.go`, `se*/a.go`,
	} {
		command := `git log -p -- '` + glob + `'`
		for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
			if _, d := patternGlobCase(t, p, command, m); d.Kind != Ask || PersonOnly(d) {
				t.Errorf("%s: %q: %+v, want an ask of the lead", m, command, d)
			}
		}
	}
}

// A pathspec glob that only might cover a denied file goes to the lead: its
// text says nothing of that file. One aimed at a denied name is denied above.
func TestGitGlobThatOnlyMightMatchAsks(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		`git diff -- 'src/*'`,
		`git diff -- src/*`,
		`git log -p -- 'src/*'`,
		`git log -- 'internal/*'`,
		`git show HEAD -- 'src/*'`,
		`git status src/*`,
		`git diff -- '.github/*'`,
		`git diff -- 'src/s*'`,
		`git diff -- '*'`,
		`git log -- 'se*'`,
		`git diff -- '*.go'`,
		`git diff -- 'src/*.go'`,
		`git diff -- 'src/**/*.go'`,
		`git diff -- .github/workflows/*.yml`,
		`git diff -- '*/x'`,
	} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			if _, d := patternGlobCase(t, p, command, m); d.Kind != Ask || PersonOnly(d) {
				t.Errorf("%s: %q: %+v, want an ask of the lead", m, command, d)
			}
		}
	}
	// Plan denies any line with a (, as it did before pathspec globs were read.
	command := `git diff -- ':(glob)src/*'`
	for _, m := range []mode.Mode{mode.Ask, mode.Edits, mode.Full} {
		if _, d := patternGlobCase(t, p, command, m); d.Kind != Ask || PersonOnly(d) {
			t.Errorf("%s: %q: %+v, want an ask of the lead", m, command, d)
		}
	}
	if _, d := patternGlobCase(t, p, command, mode.Plan); d.Kind != Deny {
		t.Errorf("plan: %q: %+v, want a deny", command, d)
	}
}

// decisionRank orders decisions from the most open to the most closed.
func decisionRank(kind string) int {
	return map[string]int{Allow: 0, Ask: 1, Deny: 2}[kind]
}

// The shell turns an unquoted glob that starts with - into an option when a
// file has such a name, and a file named -L1,1:.env or --output=x is enough:
// on a Git read it gets at least the decision that the word gets on grep.
func TestGitOptionShapedGlobIsNoPlainRead(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, word string }{
		{`git log -*`, `-*`},
		{`git diff -*`, `-*`},
		{`git show -*`, `-*`},
		{`git status -*`, `-*`},
		{`git log -?1,1:.env`, `-?1,1:.env`},
	} {
		for _, role := range []string{"implement", "review"} {
			for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
				root := t.TempDir()
				git := Action{Kind: "exec", Target: tc.command, Role: role, Work: root, Cwd: root}
				grep := git
				grep.Target = "grep " + tc.word + " f"
				d, onGrep := Evaluate(p, LeadRules{}, m, git), Evaluate(p, LeadRules{}, m, grep)
				if d.Kind == Allow || decisionRank(d.Kind) < decisionRank(onGrep.Kind) {
					t.Errorf("%s %s: %q: %+v, want at least the decision on grep: %+v", role, m, tc.command, d, onGrep)
				}
			}
		}
	}
}

// A glob quoted inside an option value is no file the shell can find, so it
// keeps the decision it had: --include and -G values stay with their tools.
func TestGitQuotedOptionValueKeepsItsDecision(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, want string }{
		{`git log --format=%h`, Allow},
		{`git log --format="%h"`, Allow},
		{`git log --grep='fix*'`, Ask},
		{`git log '-*'`, Ask},
	} {
		if _, d := patternGlobCase(t, p, tc.command, mode.Plan); d.Kind != tc.want {
			t.Errorf("%q: %+v, want %s", tc.command, d, tc.want)
		}
	}
}

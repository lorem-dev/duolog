package policy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// A glob option's value written as its own word is no target, so it cannot
// take the operand of the same name out of the walk. A word that only looks
// like such an option, as the pattern of -e does, takes nothing out either.
func TestGrepOptionValueHidesNoRoot(t *testing.T) {
	p := policy(t)
	tree := []string{"sub2/.env", "sub2/server.key", "clean/a.go"}
	for _, command := range []string{
		`grep -rn TOKEN --exclude sub2 sub2 clean`,
		`grep -rn TOKEN --exclude sub2 clean sub2`,
		`grep -rn TOKEN --include=*.env --exclude sub2 sub2 clean`,
		`grep -rn TOKEN --exclude=sub2 sub2 clean`,
		`grep -rn -e --exclude=sub2 --exclude-dir=.cache sub2 clean`,
	} {
		for _, m := range allModes {
			if d := treeCase(t, p, tree, command, m); d.Kind == Allow {
				t.Errorf("%s: %q: %+v, want no allow", m, command, d)
			}
		}
	}
	clean := []string{"sub2/a.go", "clean/a.go"}
	for _, command := range []string{
		`grep -rn TOKEN --exclude sub2 sub2 clean`,
		`grep -rn TOKEN --exclude=sub2 sub2 clean`,
		`grep -rn TOKEN clean --exclude-dir=sub2`,
	} {
		for _, m := range allModes {
			if d := treeCase(t, p, clean, command, m); d.Kind != Allow {
				t.Errorf("%s: %q on a clean tree: %+v, want an allow", m, command, d)
			}
		}
	}
}

// BSD grep also matches --include against the path, where * crosses a slash,
// so the glob selects nothing that the walk may skip.
func TestGrepIncludeHidesNoFile(t *testing.T) {
	p := policy(t)
	tree := []string{"sub2/.env", "sub2/server.key", "cmd2/secrets/a.go", "cmd2/a.go", "clean/a.go"}
	for _, command := range []string{
		`grep -rn TOKEN sub2 --include=*sub2*`,
		`grep -rn TOKEN cmd2 --include=*secrets*`,
		`grep -rn TOKEN cmd2 --include=cmd2/secrets/*`,
		`grep -rn TOKEN sub2 --include=*.go`,
		`grep -rn TOKEN cmd2 --include=*.go`,
	} {
		for _, m := range allModes {
			if d := treeCase(t, p, tree, command, m); d.Kind == Allow {
				t.Errorf("%s: %q: %+v, want no allow", m, command, d)
			}
		}
	}
	for _, m := range allModes {
		if d := treeCase(t, p, tree, `grep -rn TOKEN clean --include=*.go`, m); d.Kind != Allow {
			t.Errorf("%s: a clean root beside denied files: %+v, want an allow", m, d)
		}
	}
}

// fromFolder evaluates command in the folder sub of a new work root that holds
// files.
func fromFolder(t *testing.T, p config.Policy, files []string, sub, command string, m mode.Mode) Decision {
	t.Helper()
	root := t.TempDir()
	for _, name := range append([]string{sub + "/keep"}, files...) {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cwd := filepath.Join(root, filepath.FromSlash(sub))
	return Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: cwd})
}

// Git reads :(top) and :/ from the top of the repository, wherever the command
// runs, so the tree that shows what the glob means is the one at the top.
func TestTopPathspecIsCheckedFromTheTop(t *testing.T) {
	p := policy(t)
	for _, pathspec := range []string{`:(top)a.p*`, `:/a.p*`} {
		command := `git log -p -- '` + pathspec + `'`
		for _, m := range allModes {
			if d := fromFolder(t, p, []string{"a.pem", "only/a.py"}, "only", command, m); d.Kind != Deny {
				t.Errorf("%s: %q from a folder: %+v, want a deny", m, command, d)
			}
		}
		if d := fromFolder(t, p, []string{"a.py", "only/a.py"}, "only", command, mode.Edits); d.Kind != Ask {
			t.Errorf("%q with only allowed matches: %+v, want an ask", command, d)
		}
	}
}

// A plain path of a :(top) pathspec is judged where Git reads it, so a rule
// that names a path from the top holds in every folder.
func TestTopPathspecPathIsJudgedFromTheTop(t *testing.T) {
	p := policy(t)
	p.Rules = append([]config.Rule{{Action: "read", Match: "conf/prod.yaml", Decision: Deny}}, p.Rules...)
	files := []string{"conf/prod.yaml", "only/conf/prod.yaml"}
	for _, pathspec := range []string{`:(top)conf/prod.yaml`, `:/conf/prod.yaml`} {
		for _, form := range []string{`git log -p -- '%s'`, `git show '%s'`} {
			command := replacePercent(form, pathspec)
			if d := fromFolder(t, p, files, "only", command, mode.Edits); d.Kind != Deny {
				t.Errorf("%q from a folder: %+v, want a deny", command, d)
			}
		}
	}
	if d := fromFolder(t, p, files, "only", `git log -p -- conf/prod.yaml`, mode.Edits); d.Kind != Allow {
		t.Errorf("a path from the folder: %+v, want an allow", d)
	}
	if d := fromFolder(t, p, files, "only", `git log -p -- ':(top)conf/other.yaml'`, mode.Edits); d.Kind != Allow {
		t.Errorf("another path from the top: %+v, want an allow", d)
	}
}

func replacePercent(form, value string) string {
	for i := 0; i+1 < len(form); i++ {
		if form[i:i+2] == "%s" {
			return form[:i] + value + form[i+2:]
		}
	}
	return form
}

// Only the tilde inside a name is literal enough to pass in quotes: a leading
// one, one before a path or any other unsafe character still is not.
func TestQuotedTildeOfAShortNameIsLiteral(t *testing.T) {
	for run, want := range map[string]bool{
		"C:/Users/RUNNER~1/a.md": false,
		"PROGRA~2/x":             false,
		"~/x":                    true,
		"~1/x":                   true,
		"a~/x":                   true,
		"a~b":                    true,
		"a~1*":                   true,
		"~":                      true,
	} {
		if got := quotedRunUnsafe(run, quotedLiteralUnsafe); got != want {
			t.Errorf("%q: unsafe=%v, want %v", run, got, want)
		}
	}
	// Taking the quotes as literal shows the word to the 8.3 check.
	p := policy(t)
	for _, m := range allModes {
		if d := treeCase(t, p, nil, `cat 'DUOLOG~1/bus.ndjson'`, m); d.Kind != Deny {
			t.Errorf("%s: a quoted short name of state: %+v, want a deny", m, d)
		}
	}
}

// The top is the nearest folder that holds .git, a linked worktree's file too.
func TestGitTop(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	only := filepath.Join(nested, "only")
	if err := os.MkdirAll(only, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := gitTop(Action{Work: root, Cwd: only}); got != root {
		t.Fatalf("no .git: %s, want the work root", got)
	}
	if err := os.WriteFile(filepath.Join(nested, ".git"), []byte("gitdir: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := gitTop(Action{Work: root, Cwd: only}); got != nested {
		t.Fatalf("got %s, want %s", got, nested)
	}
}

// The account state, the kept quota reads among it, is closed to a worker in
// every mode, as the telemetry and balance stores are.
func TestAccountStoreIsClosedToWorkers(t *testing.T) {
	h := newProtectedHome(t)
	p := broadPolicy(t, h.home)
	t.Setenv("HOME", h.home)
	t.Setenv("USERPROFILE", h.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h.home, "config"))
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	accounts := filepath.Join(userDir, "duolog", "accounts")
	read := func(target string) Action {
		return Action{Kind: "read", Target: slash(target), Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()}
	}
	for _, name := range []string{"quota-reads.json", "state.json", "pace.json"} {
		file := filepath.Join(accounts, name)
		assertProtected(t, p, read(file))
		write := read(file)
		write.Kind = "write"
		assertProtected(t, p, write)
		assertProtected(t, p, Action{Kind: "exec", Target: `cat "` + slash(file) + `"`, Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()})
		assertProtected(t, p, Action{Kind: "exec", Target: `echo x > "` + slash(file) + `"`, Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()})
	}
	assertNormal(t, p, read(filepath.Join(userDir, "duolog", "other.json")))
	lead := read(filepath.Join(accounts, "quota-reads.json"))
	lead.ProtectedRoots = nil
	if d := Evaluate(p, LeadRules{}, mode.Full, lead); d.Rule == RuleProtected {
		t.Fatalf("a caller with no store roots: %+v", d)
	}
}

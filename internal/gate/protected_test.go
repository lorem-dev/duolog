package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

var allModes = []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full}

// protectedHome is a temporary home with a telemetry root that holds data and
// a balance root that does not exist yet, as before balance first runs.
type protectedHome struct {
	home, work, base, telemetry, balance string
}

func newProtectedHome(t *testing.T) protectedHome {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	base := filepath.Join(home, "state", "duolog")
	h := protectedHome{home, filepath.Join(home, "project"), base, filepath.Join(base, "telemetry"), filepath.Join(base, "balance")}
	for _, dir := range []string{filepath.Join(h.work, "src"), filepath.Join(h.telemetry, "v1", "_account"), filepath.Join(base, "telemetry-old")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(h.telemetry, "salt"), filepath.Join(h.telemetry, "v1", "_account", "2026-11-05.ndjson"), filepath.Join(h.work, "src", "a.go"), filepath.Join(base, "telemetry-old", "notes.txt")} {
		if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h protectedHome) roots() []string { return []string{h.telemetry, h.balance} }

// broadPolicy allows every read, write and command, inside and outside the
// work root, so that only the protected deny can refuse.
func broadPolicy(t *testing.T, home string) config.Policy {
	t.Helper()
	p := policy(t)
	outside := absoluteTestPath(home, "/**")
	p.Rules = append([]config.Rule{
		{Action: "read", Match: "**", Decision: Allow}, {Action: "write", Match: "**", Decision: Allow},
		{Action: "read", Match: outside, Decision: Allow}, {Action: "write", Match: outside, Decision: Allow},
		{Action: "exec", Match: "*", Decision: Allow},
	}, p.Rules...)
	return p
}

func slash(path string) string { return filepath.ToSlash(path) }

// assertProtected requires a deny by the protected rule in every mode: no
// policy, mode or ask comes first.
func assertProtected(t *testing.T, p config.Policy, a Action) {
	t.Helper()
	for _, m := range allModes {
		d := Evaluate(p, LeadRules{}, m, a)
		if d.Kind != Deny || d.Rule != RuleProtected || d.Reason == "" || d.Mode != m {
			t.Errorf("%s %q in %s: %+v, want a protected deny", a.Kind, a.Target, m, d)
		}
	}
}

// assertNormal requires the decision that the same action gets without
// protected roots.
func assertNormal(t *testing.T, p config.Policy, a Action) {
	t.Helper()
	plain := a
	plain.ProtectedRoots = nil
	for _, m := range allModes {
		got, want := Evaluate(p, LeadRules{}, m, a), Evaluate(p, LeadRules{}, m, plain)
		if got != want || got.Rule == RuleProtected {
			t.Errorf("%s %q in %s: %+v, want %+v", a.Kind, a.Target, m, got, want)
		}
	}
}

func TestProtectedWorkerPaths(t *testing.T) {
	h := newProtectedHome(t)
	p := broadPolicy(t, h.home)
	t.Setenv("HOME", h.home)
	t.Setenv("USERPROFILE", h.home)
	resolved, err := filepath.EvalSymlinks(h.home)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(h.base, alias); err != nil {
		// Windows needs a privilege for symlinks; its test covers junctions.
		t.Log("symlinks unavailable:", err)
		alias = ""
	}
	segment := filepath.Join(h.telemetry, "v1", "_account", "2026-11-05.ndjson")
	salt := filepath.Join(h.telemetry, "salt")
	missing := filepath.Join(h.telemetry, "v1", "0123456789abcdef", "2026-11-05.ndjson")
	balance := filepath.Join(h.balance, "incumbents.json")
	// /private/var against /var on macOS: the resolved spelling of the same home.
	private := filepath.Join(resolved, "state", "duolog", "telemetry", "salt")
	relative, err := filepath.Rel(h.work, salt)
	if err != nil {
		t.Fatal(err)
	}
	read := func(target string) Action {
		return Action{Kind: "read", Target: slash(target), Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()}
	}
	write := func(target string) Action {
		a := read(target)
		a.Kind = "write"
		return a
	}
	exec := func(command string) Action {
		return Action{Kind: "exec", Target: command, Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()}
	}
	cases := map[string]Action{
		"direct read":          read(segment),
		"direct write":         write(segment),
		"root itself":          read(h.telemetry),
		"salt":                 read(salt),
		"missing descendant":   read(missing),
		"missing write":        write(missing),
		"balance":              read(balance),
		"balance write":        write(balance),
		"relative cwd":         read(relative),
		"filesystem alias":     read(private),
		"filesystem alias new": write(filepath.Join(resolved, "state", "duolog", "balance", "new.json")),
		"cat":                  exec("cat " + slash(salt)),
		"head relative":        exec("head -n 1 " + slash(relative)),
		"cat home":             exec("cat ~/state/duolog/telemetry/salt"),
		"cat $HOME":            exec(`cat "$HOME/state/duolog/telemetry/salt"`),
		"cat private":          exec("cat " + slash(private)),
		"grep -r":              exec("grep -r token " + slash(h.telemetry)),
		"rg":                   exec("rg token " + slash(h.balance)),
		"find":                 exec("find " + slash(h.telemetry) + " -name '*.ndjson'"),
		"glob":                 exec("cat " + slash(h.telemetry) + "/v1/*/*.ndjson"),
		"copy out":             exec("cp " + slash(salt) + " src/salt"),
		"write into":           exec("echo x > " + slash(filepath.Join(h.balance, "state.json"))),
		"pipeline":             exec("ls src | cat " + slash(segment)),
		"option value":         exec("grep --file=" + slash(salt) + " src/a.go"),
		"quoted":               exec(`cat "` + slash(segment) + `"`),
	}
	if alias != "" {
		cases["symlink alias"] = read(filepath.Join(alias, "telemetry", "salt"))
		cases["symlink alias write"] = write(filepath.Join(alias, "balance", "state.json"))
		cases["cat alias"] = exec("cat " + slash(filepath.Join(alias, "telemetry", "v1", "_account", "2026-11-05.ndjson")))
		cases["rg alias"] = exec("rg -uu token " + slash(alias))
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) { assertProtected(t, p, a) })
	}
	cwd := exec("cat salt v1/_account/2026-11-05.ndjson")
	cwd.Cwd = h.telemetry
	t.Run("relative to a cwd in the root", func(t *testing.T) { assertProtected(t, p, cwd) })

	// A sibling whose name starts with a root's name is not inside it.
	for name, a := range map[string]Action{
		"sibling read":  read(filepath.Join(h.base, "telemetry-old", "notes.txt")),
		"sibling write": write(filepath.Join(h.base, "telemetry-old", "new.txt")),
		"sibling name":  read(filepath.Join(h.base, "balance.md")),
		"sibling cat":   exec("cat " + slash(filepath.Join(h.base, "telemetry-old", "notes.txt"))),
		"project read":  read("src/a.go"),
		"project cat":   exec("cat src/a.go"),
	} {
		t.Run(name, func(t *testing.T) { assertNormal(t, p, a) })
	}
}

func TestProtectedAncestorSearch(t *testing.T) {
	h := newProtectedHome(t)
	p := broadPolicy(t, h.home)
	t.Setenv("HOME", h.home)
	t.Setenv("USERPROFILE", h.home)
	exec := func(command, cwd string) Action {
		return Action{Kind: "exec", Target: command, Role: "implement", Work: h.work, Cwd: cwd, ProtectedRoots: h.roots()}
	}
	grep := func(target string) Action {
		return Action{Kind: "read", Target: slash(target), Role: "implement", Search: true, Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()}
	}
	for name, a := range map[string]Action{
		"grep -r home":     exec("grep -r token "+slash(h.home), h.work),
		"grep -R parent":   exec("grep -R token ..", h.work),
		"rg base":          exec("rg token "+slash(h.base), h.work),
		"rg hidden home":   exec("rg --hidden token ~", h.work),
		"rg in cwd":        exec("rg token", h.base),
		"grep -r in cwd":   exec("grep -rn token .", filepath.Join(h.home, "state")),
		"find home":        exec("find "+slash(h.home)+" -name '*.ndjson'", h.work),
		"find state":       exec("find ../state", h.work),
		"tree":             exec("tree "+slash(h.base), h.work),
		"balance parent":   exec("rg token "+slash(filepath.Dir(h.balance)), h.work),
		"grep tool home":   grep(h.home),
		"grep tool parent": grep(".."),
		"grep tool root":   grep(h.telemetry),
	} {
		t.Run(name, func(t *testing.T) { assertProtected(t, p, a) })
	}
	for name, a := range map[string]Action{
		"rg project":     exec("rg token src", h.work),
		"rg dot":         exec("rg token .", h.work),
		"grep -r dot":    exec("grep -r token .", h.work),
		"find project":   exec("find . -name '*.go'", h.work),
		"sibling search": exec("rg token "+slash(filepath.Join(h.base, "telemetry-old")), h.work),
		"grep tool src":  grep("src"),
		"grep tool dot":  grep("."),
	} {
		t.Run(name, func(t *testing.T) { assertNormal(t, p, a) })
	}
}

func TestProtectedCommandForms(t *testing.T) {
	for _, tc := range []struct{ command, shell string }{
		{"duolog telemetry", ""},
		{"duolog telemetry status", ""},
		{"duolog telemetry show --since 7d --json", ""},
		{"duolog telemetry purge", ""},
		{"duolog balance", ""},
		{"duolog balance show", ""},
		{"/usr/local/bin/duolog telemetry status", ""},
		{"scripts/dev/bin/duolog balance", ""},
		{"./scripts/dev/bin/duolog telemetry off", ""},
		{`"duolog" telemetry status`, ""},
		{`'/opt/duolog/bin/duolog' 'balance'`, ""},
		{`duolog "telemetry" show`, ""},
		{"env DUOLOG_X=1 duolog telemetry status", ""},
		{"sh -c 'duolog telemetry show'", ""},
		{`bash -c "duolog balance"`, ""},
		{"time duolog balance", ""},
		{"nohup duolog telemetry show", ""},
		{"echo x | duolog telemetry show", ""},
		{"true && duolog balance", ""},
		{"go test ./... ; duolog telemetry status", ""},
		{"duolog.exe telemetry status", "powershell"},
		{"& duolog telemetry status", "powershell"},
		{`& 'C:\Tools\duolog.exe' balance`, "powershell"},
		{`& "C:\Program Files\duolog\duolog.exe" telemetry show`, "powershell"},
		{`C:\Tools\duolog.exe telemetry`, "powershell"},
		{"Get-Date; duolog balance", "powershell"},
		// Global flags and their values come before the subcommand.
		{"duolog --lead L telemetry show", ""},
		{"duolog --lead=L telemetry show", ""},
		{"scripts/dev/bin/duolog --lead lead-1 --lead=lead-2 balance", ""},
		{`duolog "--lead" "L" telemetry status`, ""},
		{"duolog -h telemetry", ""},
		{"duolog --help balance", ""},
		{"duolog -- telemetry show", ""},
		{"sh -c 'duolog --lead L telemetry show'", ""},
		{"& duolog.exe --lead L telemetry status", "powershell"},
		// The shell removes redirects before duolog reads its arguments.
		{"duolog >/dev/null telemetry show", ""},
		{"duolog 2> err.log --lead L telemetry show", ""},
		{"duolog 2>&1 balance", ""},
		// A value that names a store command fails closed, whatever cobra
		// would make of it.
		{"duolog --lead telemetry show", ""},
	} {
		if !protectedCommand(tc.command, tc.shell) {
			t.Errorf("%q in %q: not protected", tc.command, tc.shell)
		}
	}
	for _, command := range []string{
		"duolog doctor", "duolog status", "duolog task list", "duolog-telemetry show",
		"rg telemetry internal", "go test ./internal/telemetry", "cat docs/balance.md", "duologx balance",
		"duolog --lead L doctor", "duolog --lead=L status telemetry", "duolog --lead L task list",
	} {
		if protectedCommand(command, "") {
			t.Errorf("%q: protected", command)
		}
	}

	// The command deny comes before policy and modes, and needs no roots.
	h := newProtectedHome(t)
	p := broadPolicy(t, h.home)
	for _, command := range []string{"duolog telemetry status", "scripts/dev/bin/duolog balance", "sh -c 'duolog telemetry show'", "echo x | duolog telemetry show", "duolog --lead L telemetry show", "duolog --lead=L balance"} {
		for _, roots := range [][]string{nil, h.roots()} {
			assertProtected(t, p, Action{Kind: "exec", Target: command, Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: roots})
		}
	}
	for _, command := range []string{"& duolog telemetry status", `& 'C:\Tools\duolog.exe' balance`} {
		assertProtected(t, p, Action{Kind: "exec", Target: command, Shell: "powershell", Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()})
	}
	assertNormal(t, p, Action{Kind: "exec", Target: "duolog doctor", Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()})
	assertNormal(t, p, Action{Kind: "exec", Target: "duolog --lead L doctor", Role: "implement", Work: h.work, Cwd: h.work, ProtectedRoots: h.roots()})
}

// TestProtectedRootsWithoutBase fails closed: a relative path that cannot be
// placed against the roots is denied, never asked about.
func TestProtectedRootsWithoutBase(t *testing.T) {
	h := newProtectedHome(t)
	p := broadPolicy(t, h.home)
	d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "read", Target: "src/a.go", Role: "implement", ProtectedRoots: h.roots()})
	if d.Kind != Deny || d.Rule != RuleProtected || !strings.Contains(d.Reason, "cannot place") {
		t.Fatal(d)
	}
}

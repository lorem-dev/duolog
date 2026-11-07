package gate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// On a POSIX shell a backslash is part of a word, never a path separator: a
// regex such as \|cfg\. is not the absolute path /|cfg/. The shell may also
// drop a backslash as an escape, so a word is outside when either reading of
// it is.
func TestBackslashIsNoSeparatorOnAPOSIXShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows host reads a backslash as a separator")
	}
	work := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(work, "lnk")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{`grep -n "^func \|cfg\." internal/doctor/doctor.go`, false},
		{`grep -n '\|cfg\.' internal/doctor/doctor.go`, false},
		{`rg -n "\.Waiting\b\|SetWaiting" internal`, false},
		{`grep x ..\..\etc\passwd`, false},
		{`grep x lnk\b`, false},
		// The escape reading: the shell passes /etc/passwd, ../x and ~/x.
		{`grep x \/etc/passwd`, true},
		{`grep x .\./x`, true},
		{`cat ~\/x`, true},
		// Outside in either reading.
		{`grep x /etc\passwd`, true},
		{`grep x ../a\b`, true},
		{`cat lnk/a\b`, true},
		// A drive letter still names a Windows root.
		{`cat C:\x`, true},
	} {
		if got := ExecOutside(Action{Kind: "exec", Target: tc.command, Work: work, Cwd: work}); got != tc.want {
			t.Errorf("%s: outside %v, want %v", tc.command, got, tc.want)
		}
	}
}

// PowerShell, and any shell on a Windows host, keeps reading a backslash as a
// separator.
func TestBackslashStaysASeparatorInPowerShell(t *testing.T) {
	work := t.TempDir()
	for _, command := range []string{`Select-String x ..\secret`, `Get-Content ..\..\x`} {
		if !ExecOutside(Action{Kind: "exec", Target: command, Work: work, Cwd: work, Shell: "powershell"}) {
			t.Errorf("%s: not outside", command)
		}
	}
}

// Commands that workers ran and that asked the person only because a regex
// held backslashes. Each now reaches no person-only ask.
func TestRegexBackslashesNoLongerAskThePerson(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows host reads a backslash as a separator")
	}
	p := testPolicy(t)
	work := t.TempDir()
	for _, command := range []string{
		`cat internal/config/defaults.go | head -40; grep -n "^func \|cfg\." internal/doctor/doctor.go | head -60; grep -n "yaml" go.mod`,
		`git grep -n "\.Waiting\b\|SetWaiting\|IsWaiting" -- '*.go' | grep -v "_test.go" ; grep -n "Stop\|events(\|\"SessionEnd\"" internal/install/install_test.go cmd/duolog/install_test.go | head -20`,
		`git grep -n "\.watch(" -- 'internal/peer/*.go' ':!*_test.go'; grep -n "lint-exceptions\|lint-go\|docs-lint" -A6 Makefile | head -50`,
		`git grep -n "^func (r \*Registry)" -- internal/session | cut -c1-160; git grep -n "func Address" -- internal/session`,
		`git grep -n "func ReportPath\|func (e \*env) taskEnded\|func pendingAsk\|func Send(\|func List(" -- 'internal/*.go'; ls docs; git grep -n "Stop hook\|turn ends\|turn end" -- docs | head`,
	} {
		for _, m := range []mode.Mode{mode.Edits, mode.Ask} {
			d := Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement", Work: work, Cwd: work, State: work})
			if PersonOnly(d) || d.Kind == Deny {
				t.Errorf("%s in %s: %+v", command, m, d)
			}
		}
	}
}

const (
	ownSession   = "0f1e2d3c-4b5a-4968-8778-695a4b3c2d1e"
	otherSession = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

// claudeTempFixture lays out a Claude temp base with the session's own
// folder, another session's folder, a symlink out of the own folder and a
// session folder that is itself a symlink.
type claudeTempFixture struct {
	tmp, own, other, project, outside, linkedSession string
}

func newClaudeTempFixture(t *testing.T) claudeTempFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Claude's session temp folders are a POSIX layout")
	}
	tmp := t.TempDir()
	f := claudeTempFixture{tmp: tmp, outside: t.TempDir()}
	f.own = ClaudeTemp(tmp, 501, ownSession, "/work/app")
	if want := filepath.Join(pathutil.Resolved(tmp), "claude-501", "-work-app", ownSession); f.own != want {
		t.Fatalf("own temp %q, want %q", f.own, want)
	}
	f.project = filepath.Dir(f.own)
	f.other = filepath.Join(f.project, otherSession)
	f.linkedSession = filepath.Join(f.project, "1b2c3d4e-0000-4000-8000-000000000000")
	for _, dir := range []string{filepath.Join(f.own, "scratchpad"), filepath.Join(f.own, "links"), filepath.Join(f.other, "scratchpad")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{
		filepath.Join(f.own, "scratchpad", "out.txt"), filepath.Join(f.other, "scratchpad", "out.txt"),
		filepath.Join(f.project, "notes.txt"), filepath.Join(f.outside, "secret"),
	} {
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A symlink that stays inside the folder is harmless; links/esc leaves it.
	if err := os.Symlink("out.txt", filepath.Join(f.own, "scratchpad", "same")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.outside, filepath.Join(f.own, "links", "esc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.outside, f.linkedSession); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestInClaudeTemp(t *testing.T) {
	f := newClaudeTempFixture(t)
	temps := []string{f.own}
	// The base as the caller spells it, which may hold a symlink such as
	// /tmp for /private/tmp.
	spelled := filepath.Join(f.tmp, "claude-501", "-work-app", ownSession)
	for _, tc := range []struct {
		name, target string
		want         bool
	}{
		{"own file", filepath.Join(f.own, "scratchpad", "out.txt"), true},
		{"own subfolder", filepath.Join(f.own, "scratchpad"), true},
		{"symlink that stays", filepath.Join(f.own, "scratchpad", "same"), true},
		{"own file through the base's spelling", filepath.Join(spelled, "scratchpad", "out.txt"), true},
		// A folder read can follow a symlink below it (grep -R, rg --follow).
		{"own folder holding a symlink out", f.own, false},
		{"subfolder holding a symlink out", filepath.Join(f.own, "links"), false},
		{"another session", filepath.Join(f.other, "scratchpad", "out.txt"), false},
		{"another session folder", f.other, false},
		{"project folder", f.project, false},
		{"file in the project folder", filepath.Join(f.project, "notes.txt"), false},
		{"uid folder", filepath.Dir(f.project), false},
		{"dotdot that stays", f.own + "/scratchpad/../scratchpad/out.txt", false},
		{"dotdot that leaves", f.own + "/../" + otherSession + "/scratchpad/out.txt", false},
		{"dotdot after a symlink", f.own + "/links/esc/../secret", false},
		{"symlink out", filepath.Join(f.own, "links", "esc", "secret"), false},
		{"symlink out itself", filepath.Join(f.own, "links", "esc"), false},
		{"symlinked session folder", filepath.Join(f.linkedSession, "secret"), false},
		{"missing file", filepath.Join(f.own, "scratchpad", "none.txt"), false},
		{"relative", "scratchpad/out.txt", false},
	} {
		if got := InClaudeTemp(temps, tc.target); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	if InClaudeTemp([]string{ClaudeTemp(f.tmp, 501, filepath.Base(f.linkedSession), "/work/app")}, filepath.Join(f.linkedSession, "secret")) {
		t.Error("a session folder that is a symlink counted as the session's own")
	}
	if InClaudeTemp(nil, filepath.Join(f.own, "scratchpad", "out.txt")) {
		t.Error("no temp folder allowed a read")
	}
	for _, id := range []string{"", "../x", "a/b"} {
		if temp := ClaudeTemp(f.tmp, 501, id, "/work/app"); temp != "" {
			t.Errorf("session id %q named %q", id, temp)
		}
	}
}

// A named read command of the worker's own temp folder counts as its own; any
// other use of the folder, and every other folder, still asks the person.
func TestReadCommandOfOwnClaudeTemp(t *testing.T) {
	f := newClaudeTempFixture(t)
	p := testPolicy(t)
	work := t.TempDir()
	action := func(command string) Action {
		return Action{Kind: "exec", Target: command, Role: "implement", Work: work, Cwd: work, State: work, Temp: []string{f.own}}
	}
	own := filepath.Join(f.own, "scratchpad", "out.txt")
	for _, command := range []string{
		"cat " + own,
		"head -5 " + own,
		"ls " + filepath.Join(f.own, "scratchpad"),
		"git status --short; ls " + filepath.Join(f.own, "scratchpad") + " 2>&1",
		// ls lists names and follows no symlink below the folder.
		"ls " + f.own,
		"ls -la " + filepath.Join(f.own, "links"),
	} {
		check(t, p, LeadRules{}, mode.Edits, action(command), Allow)
	}
	for _, command := range []string{
		"cat " + filepath.Join(f.other, "scratchpad", "out.txt"),
		"ls " + f.project,
		"cat " + f.own + "/scratchpad/../scratchpad/out.txt",
		"cat " + filepath.Join(f.own, "links", "esc", "secret"),
		"rg --follow x " + filepath.Join(f.own, "links"),
		// ls -R and -L can follow the symlink out.
		"ls -R " + f.own,
		"ls -L " + filepath.Join(f.own, "links"),
		"ls --dereference " + filepath.Join(f.own, "links"),
		"ls " + filepath.Join(f.own, "links", "esc"),
		"cat " + own + " " + filepath.Join(f.outside, "secret"),
		// Exec commands are unchanged: only reads count as the worker's own.
		"cp " + own + " copy.txt",
		"python3 " + own,
	} {
		for _, m := range []mode.Mode{mode.Edits, mode.Ask} {
			if d := Evaluate(p, LeadRules{}, m, action(command)); !PersonOnly(d) {
				t.Errorf("%s in %s: %+v, want the person's ask", command, m, d)
			}
		}
	}
	// Without the hook's temp folder, the own folder is outside as before.
	plain := action("cat " + own)
	plain.Temp = nil
	if d := Evaluate(p, LeadRules{}, mode.Edits, plain); !PersonOnly(d) {
		t.Errorf("no temp folder: %+v", d)
	}
}

package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

// Windows reads a rooted path without a drive, such as /etc/passwd or \x,
// from the root of the current drive. The gate joined it to the current
// folder, so on Windows cat /dev/zero read as dev/zero inside the work root
// and a policy rule for cat allowed it. Such a path is outside unless it
// leads back under the work root. The Windows path rules hold on any host.
func TestRootedPathWithoutDriveIsOnTheCurrentDrive(t *testing.T) {
	for _, tc := range []struct{ cwd, target, want string }{
		{`C:\work\root`, `/etc/passwd`, `C:/etc/passwd`},
		{`C:\work\root\sub`, `\x`, `C:/x`},
		{`\\srv\share\root`, `/x`, `//srv/share/x`},
		// A drive, a share or a relative path stays as written.
		{`C:\work\root`, `D:/x`, `D:/x`},
		{`C:\work\root`, `//srv/share/x`, `//srv/share/x`},
		{`C:\work\root`, `x/y`, `x/y`},
		// A current folder without a drive, as on a POSIX host.
		{`/work/root`, `/etc/passwd`, `/etc/passwd`},
	} {
		if got := onCurrentDrive(tc.cwd, tc.target); got != tc.want {
			t.Errorf("onCurrentDrive(%q, %q) = %q, want %q", tc.cwd, tc.target, got, tc.want)
		}
	}
	for _, tc := range []struct {
		target  string
		outside bool
	}{
		{`/etc/passwd`, true},
		{`\x`, true},
		{`/dev/zero`, true},
		{`/work/rootx/a`, true},
		{`/work/root/a`, false},
		{`\work\root\sub\..\b`, false},
	} {
		got := RelativeTarget(`C:\work\root`, `C:\work\root\sub`, tc.target)
		if outside(got) != tc.outside {
			t.Errorf("RelativeTarget(%q) = %q, outside %v, want %v", tc.target, got, outside(got), tc.outside)
		}
	}
}

// A POSIX escape is a character of its word, not the root of a drive: bash,
// Git Bash on Windows too, passes grep \{a,b} the pattern {a,b} and ends
// find's -exec at \;. The gate read them as \{a,b} and \ from the root of the
// current drive, so on Windows both lines needed the person. A backslash
// before a character of a file name, as in \x, still starts at the root of the
// drive, and PowerShell and cmd have no such escapes. The work root is a
// Windows one, so this runs on every host.
func TestEscapeIsNotARootedPath(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct {
		shell, command string
		outside        bool
	}{
		{"", `grep \{a,b} go.mod`, false},
		{"", `find . -name x -exec cat {} \;`, false},
		{"", `cat \{a,b}`, false},
		{"", `cat x\;`, false},
		{"", `cat \x`, true},
		{"", `cat /etc/passwd`, true},
		{"", `cat \.\.\x`, true},
		{"", `cat \{a,b}\x`, false},
		{"", `cat \\x`, true},
		{"powershell", `cat \{a,b}`, true},
	} {
		a := Action{Kind: "exec", Target: tc.command, Role: "implement", Shell: tc.shell, Work: `C:\work\root`, Cwd: `C:\work\root`}
		if got := ExecOutside(a); got != tc.outside {
			t.Errorf("%s %q: ExecOutside %v, want %v", tc.shell, tc.command, got, tc.outside)
		}
		if d := Evaluate(p, LeadRules{}, mode.Edits, a); PersonOnly(d) != tc.outside {
			t.Errorf("%s %q: %+v, want person-only %v", tc.shell, tc.command, d, tc.outside)
		}
	}
}

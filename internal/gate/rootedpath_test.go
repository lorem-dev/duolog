package gate

import "testing"

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

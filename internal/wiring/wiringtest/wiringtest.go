// Package wiringtest builds the dependencies of the commands for tests.
package wiringtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/install"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// Cmd returns the real dependencies of the commands, except where the real
// one would reach the machine of the person who runs the tests:
//
//   - a rex outside the temporary folder, such as the one in an installed Rex
//     app, stops the test before it runs (GuardRex); a test that needs a rex
//     puts a fake first on PATH;
//   - delegations admit their runs through a gate whose quota reads start no
//     binary and whose account state is in the test's temporary folder, one
//     state per working directory, so that a peer's open, send and close
//     share it;
//   - no agent counts as running above the test, so a test that answers an
//     ask in this process plays the person's terminal, whatever agent runs
//     the tests; a test of the walk starts duolog itself;
//   - install gives the lead the fallback window of LeadWindow and reads no
//     quota;
//   - Notify shows nothing on the desktop of the person who runs the tests.
func Cmd(t *testing.T) wiring.CmdDeps {
	t.Helper()
	d := wiring.Cmd()
	d.LocateRex = GuardRex(d.LocateRex)
	d.Delegate.PaidGate = paidrunstest.ByWorkingDir(t.TempDir())
	d.AgentAbove = func([]string) bool { return false }
	d.Install.LeadWindow = LeadWindow
	d.Notify = func(string, string) {}
	return d
}

// LeadWindow is install's LeadWindow for tests: the default fallback, with no
// plan read, so that no Claude binary starts.
func LeadWindow(string) (install.LeadWindow, error) {
	return install.LeadWindow{Window: 250000}, nil
}

// GuardRex wraps a rex lookup for tests: a rex outside the temporary folder
// panics before it runs.
func GuardRex(locate func() (string, bool)) func() (string, bool) {
	return func() (string, bool) {
		bin, ok := locate()
		if ok && !inTempDir(bin) {
			panic("doctor found " + bin + ", not a fake: put a fake rex first on PATH with fakeRexFirst")
		}
		return bin, ok
	}
}

func inTempDir(name string) bool {
	rel, err := filepath.Rel(pathutil.Resolved(os.TempDir()), pathutil.Resolved(name))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

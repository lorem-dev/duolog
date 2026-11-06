//go:build windows

package runner

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// A detached run has a console of its own that shows no window, so the git and
// rex it starts flash no window either; Ctrl+C of the caller's console does
// not reach it.
func TestDetachShowsNoWindow(t *testing.T) {
	cmd := exec.Command("cmd")
	Detach(cmd)
	flags := cmd.SysProcAttr.CreationFlags
	if flags&windows.CREATE_NO_WINDOW == 0 || flags&windows.CREATE_NEW_PROCESS_GROUP == 0 || flags&windows.DETACHED_PROCESS != 0 {
		t.Fatalf("creation flags %#x", flags)
	}
}

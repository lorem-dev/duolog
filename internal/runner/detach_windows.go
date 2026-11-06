//go:build windows

package runner

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Detach makes cmd start with a console of its own that shows no window, and
// in a process group of its own, so that it outlives the hook or command that
// starts it and no Ctrl+C of that console reaches it. The console programs it
// starts in turn, such as git and rex, share its hidden console and flash no
// window; under DETACHED_PROCESS each of them would open one.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
}

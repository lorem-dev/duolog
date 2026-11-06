//go:build windows

package runner

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Detach makes cmd start without a console and in a process group of its
// own, so that it outlives the hook or command that starts it and no Ctrl+C
// of that console reaches it.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}

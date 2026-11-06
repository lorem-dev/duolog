//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

// Detach makes cmd start in a session of its own, so that it outlives the
// hook or command that starts it and no terminal signal reaches it.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

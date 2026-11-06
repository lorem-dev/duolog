//go:build !windows

package quota

import (
	"os/exec"
	"syscall"
)

// configureProcess puts a read in its own process group and ends the whole
// group when the read's context ends, so that a child of the agent does not
// outlive an interrupt or a timeout.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

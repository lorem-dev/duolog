//go:build darwin

package paidruns

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// processBirth is the start time of process pid, which a later process at
// the same ID cannot share.
func processBirth(pid int) (string, bool) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(k.Proc.P_pid) != pid {
		return "", false
	}
	start := k.Proc.P_starttime
	if start.Sec == 0 && start.Usec == 0 {
		return "", false
	}
	return fmt.Sprintf("darwin:%d.%06d", start.Sec, start.Usec), true
}

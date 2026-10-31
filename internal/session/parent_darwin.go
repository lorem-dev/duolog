//go:build darwin

package session

import (
	"golang.org/x/sys/unix"
	"os"
)

func parentProcess(pid int) (int, string) {
	if pid == os.Getpid() {
		return os.Getppid(), ""
	}
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, ""
	}
	return int(proc.Eproc.Ppid), ""
}

//go:build !windows

package session

import (
	"errors"
	"os"
	"syscall"
)

// ProcessAlive treats an unknown PID or an inconclusive OS result as alive.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return true
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	defer p.Release()
	err = p.Signal(syscall.Signal(0))
	return !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH)
}

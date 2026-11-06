//go:build !windows

package paidruns

import (
	"errors"
	"syscall"
)

// lookProcess reports whether a process runs at pid and its birth identity.
// Signal 0 checks the ID without touching the process: no such process is
// Gone. A process of another user refuses the signal but still runs, so its
// birth decides; when that cannot be read either, it is Unknown. An ID of 0
// or less names a group of processes, never one, so it is Unknown.
func lookProcess(pid int) (Presence, string) {
	if pid <= 0 {
		return Unknown, ""
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		birth, ok := processBirth(pid)
		if !ok {
			return Unknown, ""
		}
		return Alive, birth
	case errors.Is(err, syscall.ESRCH):
		return Gone, ""
	}
	return Unknown, ""
}

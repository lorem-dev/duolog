//go:build !windows && !linux && !darwin

package session

import "os"

func parentProcess(pid int) (int, string) {
	if pid == os.Getpid() {
		return os.Getppid(), ""
	}
	return 0, ""
}

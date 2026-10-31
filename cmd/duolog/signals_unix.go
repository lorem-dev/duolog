//go:build !windows

package main

import (
	"os"
	"syscall"
)

// A worker runs in its own process group, so a terminal hangup reaches it only
// through duolog.
func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}

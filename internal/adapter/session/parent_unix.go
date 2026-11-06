//go:build !windows

package session

import "os"

// POSIX hook guards exec duolog, so its parent is the agent.
func LeadPID() int { return os.Getppid() }

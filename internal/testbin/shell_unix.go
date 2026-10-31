//go:build !windows

package testbin

import "os/exec"

// HookProcess runs the exact generated POSIX command as the agent does.
func HookProcess(command string) *exec.Cmd { return exec.Command("sh", "-c", command) }

// GitBashProcess is also useful for running Claude's explicit Bash shell on POSIX.
func GitBashProcess(command string) *exec.Cmd { return exec.Command("bash", "-c", command) }

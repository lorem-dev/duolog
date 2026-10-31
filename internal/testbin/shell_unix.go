//go:build !windows

package testbin

import "os/exec"

// HookProcess starts Claude directly and runs Codex through its POSIX shell.
func HookProcess(command string, args ...string) *exec.Cmd {
	if command == "duolog" {
		return exec.Command(command, args...)
	}
	return exec.Command("sh", "-c", command)
}

// GitBashProcess exercises Codex guards under Bash on POSIX too.
func GitBashProcess(command string) *exec.Cmd { return exec.Command("bash", "-c", command) }

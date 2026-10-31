package testbin

import (
	"os/exec"
	"strings"
	"syscall"
)

// HookProcess starts Claude directly. Codex names
// PowerShell in commandWindows; pass that exact command line to CreateProcess
// rather than guessing Codex's undocumented outer shell or re-quoting argv.
func HookProcess(command string, args ...string) *exec.Cmd {
	if command == "duolog" {
		return exec.Command(command, args...)
	}

	if strings.HasPrefix(command, "powershell -NoProfile -NonInteractive -Command ") {
		cmd := exec.Command("powershell")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: command}
		return cmd
	}
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
}

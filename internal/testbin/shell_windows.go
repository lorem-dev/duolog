package testbin

import (
	"os/exec"
	"strings"
	"syscall"
)

// HookProcess uses Claude's explicitly selected PowerShell host. Codex names
// PowerShell in commandWindows; pass that exact command line to CreateProcess
// rather than guessing Codex's undocumented outer shell or re-quoting argv.
func HookProcess(command string) *exec.Cmd {
	if strings.HasPrefix(command, "powershell -NoProfile -NonInteractive -Command ") {
		cmd := exec.Command("powershell")
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: command}
		return cmd
	}
	return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
}

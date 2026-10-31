//go:build windows

package session

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

func parentProcess(pid int) (int, string) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, ""
	}
	defer windows.CloseHandle(snap)
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ProcessID) == pid {
			return int(e.ParentProcessID), windows.UTF16ToString(e.ExeFile[:])
		}
	}
	return 0, ""
}

func LeadPID() int {
	pid := os.Getppid()
	for steps := 0; steps < 32; steps++ {
		next, name := parentProcess(pid)
		switch strings.ToLower(filepath.Base(name)) {
		case "cmd.exe", "powershell.exe", "pwsh.exe", "sh.exe", "bash.exe", "zsh.exe":
			if next <= 1 || next == pid {
				return pid
			}
			pid = next
		default:
			return pid
		}
	}
	return pid
}

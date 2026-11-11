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
	defer func() { _ = windows.CloseHandle(snap) }()
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ProcessID) == pid {
			return int(e.ParentProcessID), windows.UTF16ToString(e.ExeFile[:])
		}
	}
	return 0, ""
}

// processArgs is the argument vector of the process pid, from its command
// line, split as Windows splits one. It is nil when the process cannot be
// read. The buffer holds 64 KiB: a longer command line fails the query, and
// that fails closed, since a process whose arguments are not read is named
// by no script.
func processArgs(pid int) []string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // G115: Windows process ids are 32-bit
	if err != nil {
		return nil
	}
	defer func() { _ = windows.CloseHandle(h) }()
	const size = 64 << 10
	buf := make([]byte, size)
	var n uint32
	// The query fills buf with a UNICODE_STRING that points into buf itself.
	if windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), size, &n) != nil { //nolint:gosec // G103: the Windows API takes the buffer as a pointer
		return nil
	}
	line := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])).String() //nolint:gosec // G103: buf starts with the UNICODE_STRING the query wrote
	args, err := windows.DecomposeCommandLine(line)
	if err != nil {
		return nil
	}
	return args
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

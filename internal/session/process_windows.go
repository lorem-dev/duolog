//go:build windows

package session

import "golang.org/x/sys/windows"

func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return true
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err != windows.ERROR_INVALID_PARAMETER
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if windows.GetExitCodeProcess(handle, &code) != nil {
		return true
	}
	return code == 259 // STILL_ACTIVE
}

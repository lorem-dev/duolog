//go:build windows

package paidruns

import (
	"errors"
	"fmt"
	"math"

	// The package's tests declare a helper named windows.
	win "golang.org/x/sys/windows"
)

// stillActive is the exit code Windows reports for a running process.
const stillActive = 259

// lookProcess reports whether a process runs at pid and its birth identity:
// its creation time. An ID that names no process is Gone; access denied or
// any other failure is Unknown. Windows keeps an ID unused while a handle to
// its process is open, so the birth read through that handle is the
// process's own.
func lookProcess(pid int) (Presence, string) {
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return Unknown, ""
	}
	h, err := win.OpenProcess(win.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, win.ERROR_INVALID_PARAMETER) {
			return Gone, ""
		}
		return Unknown, ""
	}
	defer win.CloseHandle(h)
	var code uint32
	if win.GetExitCodeProcess(h, &code) != nil {
		return Unknown, ""
	}
	if code != stillActive {
		return Gone, ""
	}
	var created, exited, kernel, user win.Filetime
	if win.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return Alive, ""
	}
	return Alive, fmt.Sprintf("windows:%d", created.Nanoseconds())
}

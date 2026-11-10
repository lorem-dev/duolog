package agentsync

import (
	"os"
	"runtime"
)

// targetMatches compares content, and permission bits where the system keeps
// them. Windows reports a writable regular file as 0666 whatever was asked,
// and writeAtomic does not chmod there, so only content counts.
func targetMatches(content []byte, want string, mode, wantMode os.FileMode) bool {
	if hashBytes(content) != want {
		return false
	}
	return runtime.GOOS == "windows" || mode.Perm() == wantMode.Perm()
}

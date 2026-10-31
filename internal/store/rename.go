package store

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"time"
)

// Rename publishes a closed temporary file, retrying transient Windows reader,
// antivirus and indexer sharing violations. Never remove the destination first:
// on failure the previous file must remain intact.
func Rename(oldpath, newpath string) error {
	return renameRetry(func() error { return os.Rename(oldpath, newpath) }, runtime.GOOS == "windows")
}

func renameRetry(rename func() error, windows bool) error {
	for attempt := 0; ; attempt++ {
		err := rename()
		// Windows reports ACCESS_DENIED as well as SHARING/LOCK_VIOLATION for an
		// open destination. These numeric Win32 codes are not Unix errnos here.
		transient := windows && (errors.Is(err, syscall.Errno(5)) || errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33)))
		if !transient || attempt == 7 {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
}

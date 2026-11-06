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

// ReadFile reads a file that another process may replace with Rename. On
// Windows the open fails with a sharing violation while that rename runs, so
// it retries the same way Rename does; a missing file is reported at once.
func ReadFile(path string) ([]byte, error) {
	var data []byte
	err := renameRetry(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	}, runtime.GOOS == "windows")
	return data, err
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

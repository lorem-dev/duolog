//go:build unix

package codex

import (
	"os"

	"golang.org/x/sys/unix"
)

// openPlain opens path for reading without following a link at it, and
// without blocking when a FIFO or device took its place: readPlain then sees
// what it opened is no plain file.
func openPlain(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

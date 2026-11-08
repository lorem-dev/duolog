//go:build !unix

package codex

import (
	"fmt"
	"os"
)

// openPlain opens path for reading only when it is a plain file, and the
// file opened is the one looked at: a link or reparse point put there after
// the look is refused.
func openPlain(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a plain file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, fmt.Errorf("%s changed while duolog read it", path)
	}
	return f, nil
}

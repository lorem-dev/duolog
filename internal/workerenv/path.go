// Package workerenv keeps worker hooks on the running duolog binary.
package workerenv

import (
	"os"
	"path/filepath"
)

// Path puts this executable's directory before the parent's search path.
// Failure to locate ourselves must stop worker startup, not bypass its gate.
func Path() string {
	exe, err := os.Executable()
	if err != nil {
		panic("locate duolog for worker hooks: " + err.Error())
	}
	path := filepath.Dir(exe)
	if parent := os.Getenv("PATH"); parent != "" {
		path += string(os.PathListSeparator) + parent
	}
	return "PATH=" + path
}

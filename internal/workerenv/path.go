// Package workerenv keeps worker hooks on the running duolog binary.
package workerenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// IsWorker reports whether this process runs inside a worker. It reads the depth
// as the hook guards do: only a number above zero is a worker, so that a stray value
// never blocks the lead.
func IsWorker() bool {
	depth, _ := strconv.Atoi(os.Getenv("DUOLOG_DEPTH"))
	return depth >= 1
}

// Passthrough lists the named variables that are set. PATH is set separately, and
// DUOLOG_ and REX_ names never pass on: a worker that learns the Rex session could
// rearrange the lead's terminal.
func Passthrough(names []string) []string {
	var pairs []string
	for _, name := range names {
		if strings.EqualFold(name, "PATH") || strings.HasPrefix(name, "DUOLOG_") || strings.HasPrefix(name, "REX_") {
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			pairs = append(pairs, name+"="+value)
		}
	}
	return pairs
}

// LookPath finds the program name on the PATH of env, the environment a
// process is built with, never on duolog's own: a check or a launch runs the
// program its process would. A name with a separator is taken as a path.
// Not found is exec.ErrNotFound.
func LookPath(name string, env []string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return exec.LookPath(name)
	}
	path := ""
	for _, pair := range env {
		key, value, _ := strings.Cut(pair, "=")
		if key == "PATH" || runtime.GOOS == "windows" && strings.EqualFold(key, "PATH") {
			path = value
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if bin, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return bin, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

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

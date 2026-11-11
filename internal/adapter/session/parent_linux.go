//go:build linux

package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func parentProcess(pid int) (int, string) {
	if pid == os.Getpid() {
		return os.Getppid(), ""
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, ""
	}
	start := strings.IndexByte(string(data), '(')
	end := strings.LastIndexByte(string(data), ')')
	if start < 0 || end < start {
		return 0, ""
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 2 {
		return 0, ""
	}
	parent, _ := strconv.Atoi(fields[1])
	return parent, string(data[start+1 : end])
}

// processArgs is the argument vector of the process pid, from
// /proc/<pid>/cmdline. It is nil when the process cannot be read.
func processArgs(pid int) []string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

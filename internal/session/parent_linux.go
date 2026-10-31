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
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return 0, ""
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 2 {
		return 0, ""
	}
	parent, _ := strconv.Atoi(fields[1])
	return parent, ""
}

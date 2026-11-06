//go:build linux

package paidruns

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// processBirth is the boot of the system and the start time of process pid
// in clock ticks since that boot: field 22 of /proc/<pid>/stat. The boot
// keeps a process from an earlier boot apart.
func processBirth(pid int) (string, bool) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", false
	}
	// The command name may hold spaces and parentheses; the fields after
	// its last ')' start at field 3.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(string(data[end+1:]))
	const startField = 22 - 3
	if len(fields) <= startField {
		return "", false
	}
	if _, err := strconv.ParseUint(fields[startField], 10, 64); err != nil {
		return "", false
	}
	return "linux:" + strings.TrimSpace(string(boot)) + ":" + fields[startField], true
}

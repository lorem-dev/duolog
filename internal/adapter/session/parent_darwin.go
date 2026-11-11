//go:build darwin

package session

import (
	"bytes"
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

func parentProcess(pid int) (int, string) {
	if pid == os.Getpid() {
		return os.Getppid(), ""
	}
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, ""
	}
	return int(proc.Eproc.Ppid), unix.ByteSliceToString(proc.Proc.P_comm[:])
}

// pathAlign is the word the kernel pads the executable's path to in
// kern.procargs2, counted from the path's start.
const pathAlign = 8

// processArgs is the argument vector of the process pid, from
// kern.procargs2: argc, the executable's path, its NUL and the NULs that pad
// it to pathAlign, then the arguments, each ending in a NUL. Only that padding
// is skipped, so an empty argv[0] keeps its place. It is nil when the process
// cannot be read or the buffer holds fewer arguments than argc says.
func processArgs(pid int) []string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil
	}
	start := (end + pathAlign) / pathAlign * pathAlign
	if start > len(rest) || len(bytes.Trim(rest[end:start], "\x00")) != 0 {
		return nil
	}
	rest = rest[start:]
	// Each argument takes a byte at least, its NUL: the buffer, not argc,
	// bounds the slice.
	args := make([]string, 0, min(argc, len(rest)))
	for len(args) < argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	return args
}

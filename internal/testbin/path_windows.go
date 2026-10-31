package testbin

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// ShortPath exercises the same 8.3 TEMP aliases used by hosted Windows runners.
func ShortPath(t testing.TB, path string) string {
	t.Helper()
	long, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	input, err := syscall.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	n, err := syscall.GetShortPathName(input, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		t.Fatalf("GetShortPathName: %d %v", n, err)
	}
	short := syscall.UTF16ToString(buffer[:n])
	if strings.EqualFold(short, long) {
		t.Skip("8.3 names are disabled on this volume")
	}
	return short
}

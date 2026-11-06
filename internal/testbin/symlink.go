package testbin

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
)

// errPrivilegeNotHeld is Windows' ERROR_PRIVILEGE_NOT_HELD, which os.Symlink
// returns to an account without the symlink privilege: neither elevated nor
// in Developer Mode.
const errPrivilegeNotHeld = syscall.Errno(1314)

// Symlink makes link point to target, as os.Symlink does. On Windows without
// the symlink privilege it skips the test; any other failure fails it.
func Symlink(t testing.TB, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if runtime.GOOS == "windows" && errors.Is(err, errPrivilegeNotHeld) {
		t.Skip("creating a symlink needs a privilege this Windows account does not hold:", err)
	}
	t.Fatal(err)
}

package testbin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// CommandContext bounds native command tests and reaps their Windows descendants
// before TempDir cleanup. Killing only the parent can leave hooks holding files.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 3 * time.Second
	if runtime.GOOS == "windows" {
		cmd.Cancel = func() error {
			killCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			kill := filepath.Join(os.Getenv("SYSTEMROOT"), "System32", "taskkill.exe")
			if err := exec.CommandContext(killCtx, kill, "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err != nil {
				return cmd.Process.Kill()
			}
			return nil
		}
	}
	return cmd
}

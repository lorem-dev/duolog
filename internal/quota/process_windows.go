//go:build windows

package quota

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

// configureProcess ends the read's whole process tree when its context ends,
// so that a child of the agent does not outlive an interrupt or a timeout.
func configureProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}

package kit

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// RunCheck runs one check command in dir and returns its exit code. The
// command is split into an argument list, never read as shell text. It returns
// -1 when the command is empty or did not start. Once ctx ends, the check is
// stopped with every process it started.
func RunCheck(ctx context.Context, dir, command string) (exit int) {
	args := strings.Fields(command)
	if len(args) == 0 {
		return -1
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	// Its own process group: an interrupt reaches the check through ctx, and
	// stopping it stops the test processes it started.
	configureProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err == nil {
		return 0
	}
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	return -1
}

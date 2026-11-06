package calibration

import (
	"context"
	"os/exec"
	"strings"

	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/journal"
)

// realChecks runs check commands as processes, as the command's runner does.
func realChecks(ctx context.Context, dir string, commands []string) []journal.Check {
	return profile.RunChecks(ctx, profile.Deps{RunCheck: func(ctx context.Context, dir, command string) int {
		args := strings.Fields(command)
		if len(args) == 0 {
			return -1
		}
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = dir
		if err := cmd.Run(); err == nil {
			return 0
		}
		if cmd.ProcessState != nil {
			return cmd.ProcessState.ExitCode()
		}
		return -1
	}}, dir, commands)
}

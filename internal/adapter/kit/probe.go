package kit

import (
	"bytes"
	"context"
	"os/exec"
	"strings"

	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Metadata runs the agent binary bin with args and no input, and returns what
// it printed. It is the agent's own metadata command (--version, a model
// list), never a model call.
func Metadata(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader("")
	return cmd.Output()
}

// RunProbe starts the agent as the process p the account gate built, in dir,
// with args after p's prefix and no input, and returns its standard output.
// started gets the pid once the process runs and before RunProbe waits for it;
// when started fails, the process is killed and the error returned. The output
// comes back also with the error of a process that exited badly.
func RunProbe(ctx context.Context, p corelaunch.Process, dir string, args []string, started func(pid int) error) ([]byte, error) {
	bin, err := workerenv.LookPath(p.Prefix[0], p.Env)
	if err != nil {
		return nil, err
	}
	argv := p.Argv(args...)
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Dir = dir
	cmd.Env = p.Env
	cmd.Stdin = strings.NewReader("")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if err := started(cmd.Process.Pid); err != nil {
		// A call that cannot be recorded does not run on unseen.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	err = cmd.Wait()
	return stdout.Bytes(), err
}

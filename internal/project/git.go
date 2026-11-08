package project

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/journal"
)

// Git runs git with args and returns its standard output. env is the whole
// environment of the process when it is not nil. When it is nil, git gets the
// caller's own without the Git variables (CleanGitEnv), so that git -C answers
// for the folder it is given and not for the repository of a Git hook that
// started the caller. Standard input is not set. When git fails, the error is
// an *exec.ExitError whose Stderr holds the start of git's error output.
func Git(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if env == nil {
		env = configfile.CleanGitEnv(os.Environ())
	}
	cmd.Env = env
	return cmd.Output()
}

// GitStderr returns the error output that Git kept in err, or "" when err
// is not a failed run of git.
func GitStderr(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(exit.Stderr)
	}
	return ""
}

// Batch is a running `git cat-file --batch`: object names go to In, one per
// line, and the answers come from Out.
type Batch struct {
	In  io.WriteCloser
	Out *bufio.Reader

	cmd *exec.Cmd
}

// Close ends the input and waits for git to exit.
func (b *Batch) Close() error {
	return errors.Join(b.In.Close(), b.cmd.Wait())
}

// GitBatch starts `git cat-file --batch` in dir, with line-ending conversion
// off as journal.GitCommand has it. env is the whole environment of the
// process when it is not nil; when it is nil, git gets the caller's own
// without the Git variables, as in Git.
func GitBatch(ctx context.Context, dir string, env []string) (*Batch, error) {
	cmd := journal.GitCommand(ctx, dir, "cat-file", "--batch")
	if env == nil {
		env = configfile.CleanGitEnv(os.Environ())
	}
	cmd.Env = env
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Batch{In: in, Out: bufio.NewReader(out), cmd: cmd}, nil
}

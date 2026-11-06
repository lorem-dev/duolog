package kit

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// DefaultLoginTimeout bounds a login the person may never finish.
const DefaultLoginTimeout = 3 * time.Minute

// Output runs argv in env, nil meaning duolog's own environment, and returns
// its standard output. The lead waits for this; a check that hangs ends after
// fifteen seconds. With an env the program is the one on that env's PATH.
func Output(ctx context.Context, argv, env []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	bin := argv[0]
	if env != nil {
		var err error
		if bin, err = workerenv.LookPath(argv[0], env); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

var terminalWords = regexp.MustCompile(`(?i)\btty\b|terminal|raw mode|interactive`)

// RunLogin runs the login command argv once, within timeout (the default when
// it is not positive), and says how it ended.
func RunLogin(ctx context.Context, argv []string, timeout time.Duration) agent.LoginResult {
	if len(argv) == 0 {
		return agent.Declined
	}
	if timeout <= 0 {
		timeout = DefaultLoginTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// A login may start a helper, such as a browser bridge; a timeout ends it too.
	ProcessTree(cmd)
	cmd.Stdin = strings.NewReader("")
	// The output may hold a sign-in url; it is read for the terminal check only.
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	switch {
	case err == nil:
		return agent.LoggedIn
	case ctx.Err() == nil && terminalWords.Match(out.Bytes()):
		return agent.NeedsTerminal
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return agent.TimedOut
	}
	return agent.Declined
}

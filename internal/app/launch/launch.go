// Package launch holds what the app packages need to start an agent process
// without importing workerenv: the types of the two functions that wiring
// supplies, and the depth check.
package launch

import (
	"os"
	"strconv"

	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

// Build makes the environment, the --settings JSON and the argv prefix of one
// launch: workerenv.Build is the real one. Every launch and every check of
// that shape uses it, so that a check sees the route the launch will use.
type Build func(in corelaunch.ProcessInput) (corelaunch.Process, error)

// LookPath finds the program that a process with env would start:
// workerenv.LookPath is the real one.
type LookPath func(name string, env []string) (string, error)

// DroppedKeys lists, per agent, the route names that the agent's workers no
// longer get from environ under cfg: workerenv.DroppedKeys is the real one.
// It reads the route rule, which needs the net package, so it stays in
// workerenv and reaches the app packages through their Deps.
type DroppedKeys func(cfg *config.Config, environ []string) map[string][]string

// IsWorker reports whether this process runs inside a worker. It reads the
// depth as the hook guards do: only a number above zero is a worker, so that
// a stray value never blocks the lead.
func IsWorker() bool {
	depth, _ := strconv.Atoi(os.Getenv("DUOLOG_DEPTH"))
	return depth >= 1
}

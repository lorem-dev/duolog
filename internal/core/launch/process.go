// Package launch holds the types of one agent launch: its shape and variant,
// the process it gets and the settings layers its billing route is read from.
// It builds nothing; workerenv builds a Process from these types.
package launch

import "slices"

// Shape is how duolog starts an agent: a worker (claude -p, codex exec and
// calibration cells), an interactive peer pane, or the lead, which duolog
// only reports on.
type Shape string

// Variant says whether a launch may be billed: the free variant keeps route
// names and fast mode away, the paid one exists only with the agent's
// paid_runs opt-in.
type Variant string

const (
	Worker Shape   = "worker"
	Peer   Shape   = "peer"
	Lead   Shape   = "lead"
	Free   Variant = "free"
	Paid   Variant = "paid"
)

// Process is what a launch gets: its environment, the argv prefix with the
// global overrides, and Claude's --settings JSON. Those three may hold values
// and never reach JSON. The rest is names only: the route signals, the route
// names withheld from the process (Dropped), the subscription endpoints that
// matched, whether fast mode is pinned off, the provider env_key the process
// was built with, and a key for the verdict cache.
type Process struct {
	Agent                    string
	Shape                    Shape
	Variant                  Variant
	Env                      []string `json:"-"`
	Prefix                   []string `json:"-"`
	Launch                   []string `json:"-"`
	Settings                 string   `json:"-"`
	Signals                  []Signal
	Dropped, EndpointEntries []string
	FastPinned               bool
	ProviderEnvKey           string
	CacheKey                 string
}

// Argv is the command line of an operation: the prefix with its global
// overrides, then the operation and its own flags as given.
func (p Process) Argv(args ...string) []string {
	return append(slices.Clone(p.Prefix), args...)
}

// LaunchArgv is the command line of the launch itself: the prefix, the
// launch words the process was admitted with, then the run's own flags.
func (p Process) LaunchArgv(args ...string) []string {
	return append(append(slices.Clone(p.Prefix), p.Launch...), args...)
}

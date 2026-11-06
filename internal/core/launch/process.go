// Package launch holds the types of one agent launch: its shape and variant,
// the process it gets and the settings layers its billing route is read from.
// It builds nothing; workerenv builds a Process from these types.
package launch

import (
	"slices"

	"github.com/lorem-dev/duolog/internal/core/config"
)

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

// ProcessInput describes one launch. Environ is the environment the process
// is built from, duolog's own for a worker or peer and the observed one for
// the lead. Layers are the agent's settings layers, already read with
// ReadLayer, from the lowest precedence to the highest: SettingsLayers'
// order with the argv layers before managed policy. ProviderEnvKey is the
// env_key of Codex's default provider.
// Prefix is the program and its leading words, by default the agent's name;
// for the lead it is the observed command and stays as it is.
// ProtectedRoots are the private usage stores a Claude worker's sandbox
// denies. ExtraEnv holds NAME=value pairs duolog adds for this launch, such
// as bridged MCP servers' variables and the run id; they pass the same route
// detection as the environment, so a route name among them is withheld from
// a free process and is a paid signal of a paid one.
// Launch holds the words a worker or peer launch puts right after the prefix:
// the operation, Codex's exec for a worker, and the native permission flags
// with Codex's sandbox pins. They are part of the admitted process, so what
// the gate assessed is what starts; quota and login reads never use them.
type ProcessInput struct {
	// Plan is the Claude plan (pro, max, ...) a quota read found, empty when
	// none has: workers.compact_window auto reads it.
	Plan                           string
	Agent                          string
	Shape                          Shape
	Variant                        Variant
	Config                         *config.Config
	Locations                      Locations
	Environ                        []string
	LeadID, Role, PeerID, StateDir string
	Minimal                        bool
	ExtraSettings                  map[string]any
	Layers                         []LayerFacts
	ProviderEnvKey                 string
	Prefix                         []string
	ProtectedRoots                 []string
	ExtraEnv                       []string
	Launch                         []string
	// PluginNeeds names the Claude plugins the task needs, as "name" or
	// "name@marketplace"; a Claude worker keeps those it has enabled.
	PluginNeeds []string
}

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

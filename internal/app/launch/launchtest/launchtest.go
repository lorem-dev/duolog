// Package launchtest gives the tests of the app packages the real workerenv
// functions that their Deps take, without the tests importing workerenv: the
// app tier reaches workerenv only through Deps. Only tests import it.
package launchtest

import (
	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Build is workerenv.Build, the value of every Deps.Build.
func Build(in launch.ProcessInput) (launch.Process, error) { return workerenv.Build(in) }

// DroppedKeys is workerenv.DroppedKeys, the value of every Deps.DroppedKeys.
func DroppedKeys(cfg *config.Config, environ []string) map[string][]string {
	return workerenv.DroppedKeys(cfg, environ)
}

// LookPath is workerenv.LookPath, the value of doctor's Deps.LookPath.
func LookPath(name string, env []string) (string, error) { return workerenv.LookPath(name, env) }

// Environ is workerenv.Environ, the value of calibrate's Deps.Environ.
func Environ(cfg *config.Config, agent string, environ []string) ([]string, error) {
	return workerenv.Environ(cfg, agent, environ)
}

// GateBinary is workerenv.GateBinary, the value of every Deps.GateBinary.
func GateBinary(pathEnv string) (string, error) { return workerenv.GateBinary(pathEnv) }

// Trust is the trust reader of one agent as the app packages declare it
// (delegate.Trust); the app packages that need less take it as theirs.
type Trust interface {
	Hooks(root string, environ []string) (agent.TrustState, error)
	Folder(root string, environ []string) (bool, error)
	GateProblem(s agent.TrustState, folder bool) string
	HeldGateProblem(root string, environ []string, gate string) string
}

// Trusts are the real trust readers of the registered agents by name, the
// adapters' own, for tests that read trust from a temporary home.
func Trusts() map[string]Trust {
	return map[string]Trust{"claude": claude.Trust{}, "codex": codex.Trust{}}
}

// ClaudeFolder is claude.ClaudeFolder, the value of doctor's
// Deps.ClaudeFolder.
func ClaudeFolder(home, root string) (trusted, known bool) { return claude.ClaudeFolder(home, root) }

// RouteName is workerenv.RouteName.
func RouteName(agent, name, providerEnvKey string) bool {
	return workerenv.RouteName(agent, name, providerEnvKey)
}

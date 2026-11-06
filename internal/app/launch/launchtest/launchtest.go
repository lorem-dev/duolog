// Package launchtest gives the tests of the app packages the real workerenv
// functions that their Deps take, without the tests importing workerenv: the
// app tier reaches workerenv only through Deps. Only tests import it.
package launchtest

import (
	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/install"
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

// Installer is the installer of one agent as app/install declares it (its
// Installer), for tests of that package, which may not import the adapters.
type Installer interface {
	HookFile(scope, base, home string) string
	Hooks(existing []byte, timeout int) ([]byte, []string, error)
	SkillDir(scope, base, home string) string
	InstructionFiles(root string) (current string, all []string)
}

// Installers are the real installers of the registered agents by name.
func Installers() map[string]Installer {
	return map[string]Installer{"claude": claude.Installer{}, "codex": codex.Installer{}}
}

// LoadConfig loads the config files that apply to the project at root, as the
// commands do.
func LoadConfig(root string) (*config.Config, error) { return configfile.Load(root) }

// HasGate is install.HasGate: whether the settings file at path has a
// permission gate that applies to every tool.
func HasGate(path string) bool { return install.HasGate(path) }

// IsOurCommand is install.IsOurCommand, how duolog's own hook commands are
// recognised.
func IsOurCommand(command string) bool { return install.IsOurCommand(command) }

// HooksJSON renders the hooks document of agent, as the plugin ships it.
func HooksJSON(agentName string, timeout int) ([]byte, error) {
	if agentName == "claude" {
		return claude.HooksJSON(timeout)
	}
	return codex.HooksJSON(timeout)
}

// ClaudeFolder is claude.ClaudeFolder, the value of doctor's
// Deps.ClaudeFolder.
func ClaudeFolder(home, root string) (trusted, known bool) { return claude.ClaudeFolder(home, root) }

// RouteName is workerenv.RouteName.
func RouteName(agent, name, providerEnvKey string) bool {
	return workerenv.RouteName(agent, name, providerEnvKey)
}

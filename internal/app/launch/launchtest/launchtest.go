// Package launchtest gives the tests of the app packages the real workerenv
// functions that their Deps take, without the tests importing workerenv: the
// app tier reaches workerenv only through Deps. Only tests import it.
package launchtest

import (
	"context"
	"os"
	"runtime"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// ClearPaidEnvironment removes ambient paid-route signals before TestMain
// runs tests. Use Unsetenv: even an empty value is a signal. Tests that need
// credentials set their own with t.Setenv after this baseline is established.
func ClearPaidEnvironment() {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		key := name
		if runtime.GOOS == "windows" {
			key = strings.ToUpper(key)
		}
		if workerenv.RouteName("claude", key, "") || workerenv.RouteName("codex", key, "") {
			if err := os.Unsetenv(name); err != nil {
				panic(err)
			}
		}
	}
}

// Build is workerenv.Build, the value of every Deps.Build.
func Build(in launch.ProcessInput) (launch.Process, error) { return workerenv.Build(in) }

// DroppedKeys is workerenv.DroppedKeys, the value of every Deps.DroppedKeys.
func DroppedKeys(cfg *config.Config, environ []string) map[string][]string {
	return workerenv.DroppedKeys(cfg, environ)
}

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

// Caps is the capabilities of one agent as the app packages declare them
// (delegate.Capabilities, doctor.Caps, roster.Caps), for their tests, which
// may not import the adapters.
type Caps interface {
	Scan(ctx context.Context, root string, environ []string) (caps.Inventory, error)
	Servers(root string, environ []string) map[string]caps.Server
	Import(root string, servers map[string]caps.Server, tools map[string][]string, worker []string) (*run.MCP, map[string]string)
	Refusal(root string, s caps.Server, worker []string) string
	Limit(root string, names []string, tools map[string][]string, worker []string) (codexArgs, allowedTools []string)
	Cached(root string) caps.Inventory
	HasDuolog(root string, environ []string) bool
}

// AllCaps are the real capabilities of the registered agents by name, which
// read the home folder of the environment they are given.
func AllCaps() map[string]Caps {
	return map[string]Caps{"claude": claude.Caps{}, "codex": codex.Caps{}}
}

// CapsIn are the real capabilities of the registered agents by name with
// home in their Home field, lookup as the lead's environment, and goos as the
// system whose variable names they compare ("" is this one).
func CapsIn(home string, lookup func(string) (string, bool), goos string) map[string]Caps {
	return map[string]Caps{
		"claude": claude.Caps{Home: home, LookupEnv: lookup, GOOS: goos},
		"codex":  codex.Caps{Home: home, LookupEnv: lookup, GOOS: goos},
	}
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

// CodexInstaller is the real Codex installer with the project skills folder
// dir, as agents.codex.skills_dir sets it.
func CodexInstaller(dir string) Installer { return codex.Installer{SkillsDir: dir} }

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

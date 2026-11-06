// Package launchtest gives the tests of the app packages the real workerenv
// functions that their Deps take, without the tests importing workerenv: the
// app tier reaches workerenv only through Deps. Only tests import it.
package launchtest

import (
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

// RouteName is workerenv.RouteName.
func RouteName(agent, name, providerEnvKey string) bool {
	return workerenv.RouteName(agent, name, providerEnvKey)
}

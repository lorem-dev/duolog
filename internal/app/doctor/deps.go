package doctor

import (
	"github.com/lorem-dev/duolog/internal/app/launch"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// Deps is what doctor reaches outside the package: the account admission gate
// and the lookup of the duolog that a worker's hooks run. The wiring package
// fills every field with the real function; tests fill them with fakes. Task
// 44 adds Probes and Available.
type Deps struct {
	// PaidGate opens the account admission gate that delegate uses.
	PaidGate func(*config.Config) (*paidruns.Gate, error)
	// GateBinary finds the duolog a worker's hooks run on a PATH=... pair.
	GateBinary func(pathEnv string) (string, error)
	// Build makes a worker's process: workerenv.Build, the one Build.
	Build launch.Build
	// LookPath finds the program a built process starts: workerenv.LookPath.
	LookPath launch.LookPath
	// DroppedKeys lists the route names an agent's workers no longer get:
	// workerenv.DroppedKeys.
	DroppedKeys launch.DroppedKeys
}

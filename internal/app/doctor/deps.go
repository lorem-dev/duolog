package doctor

import (
	"path/filepath"
	"sort"

	"github.com/lorem-dev/duolog/internal/app/launch"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
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
	// Agents holds each registered agent's ports by name.
	Agents map[string]Agent
	// ClaudeFolder reports whether Claude Code trusts the folder root, from
	// its user state under home; known is false when that state cannot be
	// read. It reads a file that only Claude has, so it is no Trust method.
	ClaudeFolder func(home, root string) (trusted, known bool)
}

// Trust reads what an agent's settings and approvals say about duolog's
// hooks, as delegate.Trust does; doctor needs no HeldGateProblem.
type Trust interface {
	Hooks(root string, environ []string) (coreagent.TrustState, error)
	Folder(root string, environ []string) (bool, error)
	GateProblem(s coreagent.TrustState, folder bool) string
}

// Installer names the project instruction file of an agent: the one it reads
// now in the project at root, and every place one could have been written.
type Installer interface {
	InstructionFiles(root string) (current string, all []string)
}

// Agent holds what doctor asks of one registered agent. A nil port fails
// closed: an agent with no Trust reads as untrusted, and one with no
// Installer has no instruction file to check.
type Agent struct {
	Trust     Trust
	Installer Installer
}

// instructionPaths are the instruction files the agents read in the project at
// root, by file name, so that AGENTS.md comes before CLAUDE.md.
func (d Deps) instructionPaths(root string) []string {
	var paths []string
	for _, a := range d.Agents {
		if a.Installer != nil {
			current, _ := a.Installer.InstructionFiles(root)
			paths = append(paths, current)
		}
	}
	sort.SliceStable(paths, func(i, j int) bool { return filepath.Base(paths[i]) < filepath.Base(paths[j]) })
	return paths
}

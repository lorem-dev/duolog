package doctor

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/lorem-dev/duolog/internal/app/launch"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// Deps is what doctor reaches outside the package: the account admission gate,
// the lookup of the duolog that a worker's hooks run, and each agent's probe.
// The wiring package fills every field with the real function; tests fill them
// with fakes.
type Deps struct {
	// PaidGate opens the account admission gate that delegate uses.
	PaidGate func(*config.Config) (*paidruns.Gate, error)
	// GateBinary finds the duolog a worker's hooks run on a PATH=... pair.
	GateBinary func(pathEnv string) (string, error)
	// Build makes a worker's process: workerenv.Build, the one Build.
	Build launch.Build
	// DroppedKeys lists the route names an agent's workers no longer get:
	// workerenv.DroppedKeys.
	DroppedKeys launch.DroppedKeys
	// Names lists the registered agents in registry order; doctor loops over
	// them where it reports each agent.
	Names []string
	// Agents holds each registered agent's ports by name.
	Agents map[string]Agent
	// ClaudeFolder reports whether Claude Code trusts the folder root, from
	// its user state under home; known is false when that state cannot be
	// read. It reads a file that only Claude has, so it is no Trust method.
	ClaudeFolder func(home, root string) (trusted, known bool)
	// Probes holds each agent's Probe, by agent name.
	Probes map[string]Probe
	// Available says whether the agent's binary can be found.
	Available func(agent string) bool
	// InstallCheck compares the project install at root and the user install
	// under home with what this duolog writes, and returns a line for each
	// install that is out of date or could not be checked. Nil checks none.
	InstallCheck func(root, home string) []string
	// LeadCompact says what the project install at root set as the lead's
	// compact window, or the person's own value; "" for none. A window that
	// is missing is one of InstallCheck's lines. Nil reports none.
	LeadCompact func(root, home string) string
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

// Caps is what doctor asks of an agent's capabilities, as delegate's
// Capabilities does: the MCP servers its config defines, whether a worker of
// it could take a server, and whether duolog's hooks are in its settings.
type Caps interface {
	Servers(root string, environ []string) map[string]caps.Server
	Refusal(root string, s caps.Server, worker []string) string
	HasDuolog(root string, environ []string) bool
}

// Agent holds what doctor asks of one registered agent. A nil port fails
// closed: an agent with no Trust reads as untrusted, one with no Installer has
// no instruction file to check, and one with no Caps has no duolog hooks and
// lends no server.
type Agent struct {
	Trust     Trust
	Installer Installer
	Caps      Caps
}

// hasDuolog reports whether duolog's hooks are in agent's settings, read from
// duolog's own environment; an agent with no Caps has none.
func (d Deps) hasDuolog(agent, root string) bool {
	c := d.Agents[agent].Caps
	return c != nil && c.HasDuolog(root, os.Environ())
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

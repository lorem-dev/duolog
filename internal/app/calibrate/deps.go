package calibration

import (
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// Deps is what a cell and a judge reach outside the package: the delegation
// that runs the worker, the check that a cell's tree is isolated and the coin
// that orders a judged pair. The wiring package fills every field with the
// real function; tests fill them with fakes.
type Deps struct {
	// Delegate runs one delegation: delegate.Run bound to its Deps.
	Delegate delegate.Runner
	// Verify checks that the tree in dir shares no history or object store
	// with the repository at repo. VerifyIsolated is the real one.
	Verify func(dir, repo string) error
	// Rand returns true when the pair's A is shown first.
	Rand func() bool
	// CellCap is the time cap of one cell, CheckCap the time cap of one run of
	// a cell's checks. Zero is the cap the config gives, CellMaxMinutes.
	CellCap, CheckCap time.Duration
	// Environ is the environment a free worker of agent gets under cfg from
	// environ, as Build makes it without settings layers, extras or a lead:
	// workerenv.Environ is the real one.
	Environ func(cfg *config.Config, agent string, environ []string) ([]string, error)
	// Agents holds each registered agent's ports by name.
	Agents map[string]Agent
}

// Trust reads what an agent's settings and approvals say about duolog's
// hooks for a process whose environment is environ, as delegate.Trust does.
type Trust interface {
	Hooks(root string, environ []string) (coreagent.TrustState, error)
	Folder(root string, environ []string) (bool, error)
}

// Installer renders an agent's hooks: with a nil existing file and a gate
// timeout in seconds, the whole hooks document. It is the Installer port of
// app/install, which wiring fills; calibration needs only Hooks.
type Installer interface {
	Hooks(existing []byte, timeout int) ([]byte, []string, error)
}

// Agent holds what calibration asks of one registered agent. A nil port fails
// closed: a tree of an agent with no Trust is refused, and one with no
// Installer gets no gate written.
type Agent struct {
	Trust     Trust
	Installer Installer
}

// cellLimit is the time cap of one cell.
func (d Deps) cellLimit(c config.Calibration) time.Duration {
	if d.CellCap > 0 {
		return d.CellCap
	}
	return CheckLimit(c)
}

// checkLimit is the time cap of one run of a cell's checks.
func (d Deps) checkLimit(c config.Calibration) time.Duration {
	if d.CheckCap > 0 {
		return d.CheckCap
	}
	return CheckLimit(c)
}

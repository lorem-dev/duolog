package calibration

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
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
	// Trees holds each agent's TreeGuard, by agent name.
	Trees Trees
}

// TreeGuard is what an agent needs of a calibration tree or judge slot before
// its worker runs there. environ is the environment that agent's worker gets
// (workerenv.Environ): the settings, approvals and trusted folders read are
// those it selects, not duolog's own.
type TreeGuard interface {
	// Prepare gives dir what the agent needs from duolog, such as the gate in
	// its settings when nothing else gives it one there.
	Prepare(dir string, environ []string) error
	// Check says what the agent still lacks in dir: the gate, or the trust
	// only the person can grant. It returns nil when a worker may run there.
	Check(dir string, environ []string) error
}

// Trees are the tree guards of the agents, by agent name.
type Trees map[string]TreeGuard

// TreePreparer returns the function that prepares a tree for every agent of
// d.Trees, in name order, each with the environment its worker gets under cfg
// (WorkerEnviron). No guards prepare nothing.
func (d Deps) TreePreparer(cfg *config.Config) func(dir string) error {
	return func(dir string) error {
		for _, name := range slices.Sorted(maps.Keys(d.Trees)) {
			environ, err := d.WorkerEnviron(cfg, name)
			if err != nil {
				return err
			}
			if err := d.Trees[name].Prepare(dir, environ); err != nil {
				return err
			}
		}
		return nil
	}
}

// CheckTree asks agent's guard whether a worker of agent may run in dir, under
// the environment that worker gets under cfg. An agent with no guard is
// refused.
func (d Deps) CheckTree(cfg *config.Config, agent, dir string) error {
	guard, ok := d.Trees[agent]
	if !ok {
		return fmt.Errorf("no tree guard for agent %s", agent)
	}
	environ, err := d.WorkerEnviron(cfg, agent)
	if err != nil {
		return err
	}
	return guard.Check(dir, environ)
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

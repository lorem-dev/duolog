package calibration

import (
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

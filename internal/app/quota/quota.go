// Package quota reads the subscription quota of each agent's processes
// through that agent's Reader, and shows and keeps what the reads find: the
// quota command's report, calibration's view of the accounts and the
// observations telemetry mirrors. It reads no vendor itself.
package quota

import (
	"context"
	"errors"
	"fmt"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/budget"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

// Reader reads the quota a process of its agent sees, without a model call.
// A process of another agent is an error, never a read.
type Reader interface {
	Read(ctx context.Context, p corelaunch.Process) (account.Snapshot, error)
}

// Deps is what quota reads with: the Reader of each agent that has one, by
// agent name, and Calibration, which replaces calibration's reads in tests
// (nil reads them through the account gate).
type Deps struct {
	Agents      map[string]Reader
	Calibration budget.QuotaReader
}

// ErrNoReader is the error of a read of an agent that has no Reader.
var ErrNoReader = errors.New("no quota reader")

// Read reads the quota that p sees with the Reader of p's agent. An agent
// without one is an error, so its verdict is paid and no paid pass runs on
// it; no other agent's Reader ever reads it.
func (d Deps) Read(ctx context.Context, p corelaunch.Process) (account.Snapshot, error) {
	r := d.Agents[p.Agent]
	if r == nil {
		return account.Snapshot{}, fmt.Errorf("%w for %q", ErrNoReader, p.Agent)
	}
	return r.Read(ctx, p)
}

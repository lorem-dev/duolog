package profile

import (
	"context"

	"github.com/lorem-dev/duolog/internal/app/delegate"
)

// Deps is what profile reaches outside the package: the delegation it replays
// and judges with, the coin that orders a judged pair, and the process that
// runs one check. The wiring package fills every field with the real function;
// tests fill them with fakes.
type Deps struct {
	// Delegate runs one delegation: delegate.Run bound to its Deps.
	Delegate delegate.Runner
	// Rand returns true when the original goes first. Ordering is kept out of
	// the judge's files and is recorded only after its answer has been parsed.
	Rand func() bool
	// RunCheck runs one check command in dir and returns its exit code, or -1
	// when it did not start. kit.RunCheck is the real one.
	RunCheck func(ctx context.Context, dir, command string) (exit int)
}

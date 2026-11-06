package claude_test

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/workertest"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// TestWorkerContract runs the delegate.Worker contract on Claude's Worker and
// the fake agent binary.
func TestWorkerContract(t *testing.T) {
	delegatetest.WorkerContract(t, func(t *testing.T, c delegatetest.WorkerCase) (delegatetest.Runner, run.Spec) {
		return claude.Worker{}, workertest.Spec(t, "claude", string(c))
	})
}

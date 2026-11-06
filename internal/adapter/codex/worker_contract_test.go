package codex_test

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/adapter/workertest"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// TestWorkerContract runs the delegate.Worker contract on Codex's Worker and
// the fake agent binary.
func TestWorkerContract(t *testing.T) {
	delegatetest.WorkerContract(t, func(t *testing.T, c delegatetest.WorkerCase) (delegatetest.Runner, run.Spec) {
		return codex.Worker{}, workertest.Spec(t, "codex", string(c))
	})
}

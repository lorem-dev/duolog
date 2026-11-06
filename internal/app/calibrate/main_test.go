package calibration

import (
	"os"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// realDeps are the dependencies of the real delegations of the cell tests:
// the real functions, but an admission gate whose quota reads start no binary
// and whose account state is temporary, never the user's. TestMain sets it.
var realDeps delegate.Deps

func TestMain(m *testing.M) {
	accounts, err := os.MkdirTemp("", "duolog-accounts-")
	if err != nil {
		panic(err)
	}
	realDeps = wiring.DelegateDeps()
	realDeps.PaidGate = paidrunstest.Factory(accounts)
	code := m.Run()
	_ = os.RemoveAll(accounts)
	os.Exit(code)
}

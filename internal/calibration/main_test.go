package calibration

import (
	"os"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/delegate"
)

// TestMain gives the real delegations of the cell tests an admission gate
// whose quota reads start no binary and whose account state is temporary,
// never the user's.
func TestMain(m *testing.M) {
	accounts, err := os.MkdirTemp("", "duolog-accounts-")
	if err != nil {
		panic(err)
	}
	delegate.NewGate = paidrunstest.Factory(accounts)
	code := m.Run()
	_ = os.RemoveAll(accounts)
	os.Exit(code)
}

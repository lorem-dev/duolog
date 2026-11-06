package doctor

import (
	"os"
	"testing"

	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/paidruns/paidrunstest"
)

func TestMain(m *testing.M) {
	// Each test process starts as an independent lead; tests set worker roles explicitly.
	for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE"} {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
	// Reports and probes admit through a gate whose quota reads start no
	// binary and whose account state is temporary, never the user's.
	accounts, err := os.MkdirTemp("", "duolog-accounts-")
	if err != nil {
		panic(err)
	}
	delegate.NewGate = paidrunstest.Factory(accounts)
	code := m.Run()
	_ = os.RemoveAll(accounts)
	os.Exit(code)
}

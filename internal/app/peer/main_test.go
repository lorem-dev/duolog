package peer

import (
	"os"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
)

func TestMain(m *testing.M) {
	launchtest.ClearPaidEnvironment()
	// Each test process starts as an independent lead; tests set worker roles explicitly.
	for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE"} {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

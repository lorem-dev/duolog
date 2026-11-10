package gate

import (
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	AskPoll = 10 * time.Millisecond
	// Each test process starts as an independent lead; tests set worker roles explicitly.
	for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE"} {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

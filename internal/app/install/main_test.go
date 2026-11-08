package install

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Each test process starts as an independent lead; tests set worker roles explicitly.
	// Every install writes skills at user scope: a test gives its own home, and
	// an agent's config folder from the caller's environment must not apply.
	for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

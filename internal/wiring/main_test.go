package wiring

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Each test process starts as an independent lead; tests set worker roles explicitly.
	for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE", "CLAUDE_CONFIG_DIR", "CODEX_HOME"} {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
	// A lead's session start refreshes the duolog skills under the home: no
	// test may reach the real one. A test that needs a home sets its own.
	home, err := os.MkdirTemp("", "duolog-test-home-")
	if err != nil {
		panic(err)
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if err := os.Setenv(key, home); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

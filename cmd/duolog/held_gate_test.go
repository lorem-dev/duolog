package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// claudeGateRuns installs the duolog gate in the user's Claude settings under
// the test's HOME, and a duolog on PATH, as where duolog is installed. A
// Claude worker or peer at level plan or in a read-only role starts only
// where its gate is shown to run. Call it after the test sets HOME.
func claudeGateRuns(t *testing.T) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	gate := `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`
	if err := os.WriteFile(path, []byte(gate), 0o600); err != nil {
		t.Fatal(err)
	}
	testbin.Install(t, "duolog", testbin.Scenario{})
}

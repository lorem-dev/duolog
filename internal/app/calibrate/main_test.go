package calibration

import (
	"os"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
	"github.com/lorem-dev/duolog/internal/wiring"
)

// realDeps are the dependencies of the real delegations of the cell tests:
// the real functions, but an admission gate whose quota reads start no binary
// and whose account state is temporary, never the user's. TestMain sets it.
var realDeps delegate.Deps

// realTrees are the tree guards of the registered agents, as the cell tests
// run them.
func realTrees() Trees {
	trees := Trees{}
	for _, a := range wiring.Agents() {
		trees[a.Facts.Name] = a.Tree
	}
	return trees
}

// claudeGateJSON is the Claude settings duolog install writes: the gate on
// every tool.
func claudeGateJSON(t *testing.T) string {
	t.Helper()
	hooks, err := launchtest.HooksJSON("claude", 150)
	if err != nil {
		t.Fatal(err)
	}
	return string(hooks)
}

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

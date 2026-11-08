package doctor

import (
	"os"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/app/paidruns/paidrunstest"
)

// testDeps is what the tests pass to Run; a test that needs another gate or
// gate binary sets the field and restores it with t.Cleanup.
var testDeps Deps

func TestMain(m *testing.M) {
	launchtest.ClearPaidEnvironment()
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
	testDeps = Deps{PaidGate: paidrunstest.Factory(accounts), GateBinary: launchtest.GateBinary,
		Build: launchtest.Build, DroppedKeys: launchtest.DroppedKeys,
		PaidClaudeRoute: func(name string) bool { return launchtest.RouteName("claude", name, "") },
		ClaudeFolder:    launchtest.ClaudeFolder, Names: []string{"claude", "codex"}, Agents: map[string]Agent{}}
	installers, all := launchtest.Installers(), launchtest.AllCaps()
	for name, t := range launchtest.Trusts() {
		testDeps.Agents[name] = Agent{Trust: t, Installer: installers[name], Caps: all[name]}
	}
	code := m.Run()
	_ = os.RemoveAll(accounts)
	os.Exit(code)
}

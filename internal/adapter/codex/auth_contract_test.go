package codex_test

import (
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// TestLoginContract runs the Login contract on Codex's Login and the fake
// agent binary, which the worker's own process finds on its PATH.
func TestLoginContract(t *testing.T) {
	delegatetest.LoginContract(t, func(t *testing.T, c delegatetest.LoginCase) delegatetest.LoginSetup {
		sc := testbin.Scenario{Responses: map[string]testbin.Scenario{
			"login status":    {Stdout: "Logged in using ChatGPT\n"},
			"mcp list --json": {Stdout: `[{"name":"wiki","auth_status":"not_logged_in"}]`},
		}}
		if c == delegatetest.CaseLoggedOut {
			sc.Responses["login status"] = testbin.Scenario{Exit: 1, Stderr: "Not logged in\n"}
		}
		bin := testbin.Install(t, "codex", sc)
		p := launch.Process{Agent: "codex", Prefix: []string{"codex"}, Env: []string{"PATH=" + filepath.Dir(bin)}}
		// `codex mcp list` is read in duolog's own environment.
		t.Setenv("PATH", filepath.Dir(bin))
		s := delegatetest.LoginSetup{Login: codex.Login{}, Process: p, Server: "wiki", WantArgv: []string{"codex", "login"}}
		if c == delegatetest.CaseServerLogin {
			s.WantArgv = []string{"codex", "mcp", "login", "wiki"}
		}
		return s
	})
}

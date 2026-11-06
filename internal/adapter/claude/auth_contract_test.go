package claude_test

import (
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// TestLoginContract runs the Login contract on Claude's Login and the fake
// agent binary, which the worker's own process finds on its PATH.
func TestLoginContract(t *testing.T) {
	delegatetest.LoginContract(t, func(t *testing.T, c delegatetest.LoginCase) delegatetest.LoginSetup {
		sc := testbin.Scenario{Responses: map[string]testbin.Scenario{
			"auth status --json": {Stdout: `{"loggedIn":true}`},
			"mcp get wiki":       {Stdout: "wiki:\n  Status: ! Needs authentication\n"},
		}}
		if c == delegatetest.CaseLoggedOut {
			sc.Responses["auth status --json"] = testbin.Scenario{Stdout: `{"loggedIn":false}`, Exit: 1}
		}
		bin := testbin.Install(t, "claude", sc)
		p := launch.Process{Agent: "claude", Prefix: []string{"claude"}, Env: []string{"PATH=" + filepath.Dir(bin)}}
		// `claude mcp get` is read in duolog's own environment.
		t.Setenv("PATH", filepath.Dir(bin))
		s := delegatetest.LoginSetup{Login: claude.Login{}, Process: p, Server: "wiki", WantArgv: []string{"claude", "auth", "login"}}
		if c == delegatetest.CaseServerLogin {
			s.WantArgv = []string{"claude", "mcp", "login", "wiki"}
		}
		return s
	})
}

package delegatetest

import (
	"context"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// scriptedLogin is a Login whose answers a case scripts.
type scriptedLogin struct {
	Login
	status, server *agent.LoginProblem
}

func (l scriptedLogin) Status(context.Context, launch.Process) *agent.LoginProblem {
	return l.status
}

func (l scriptedLogin) Server(context.Context, launch.Process, string) *agent.LoginProblem {
	return l.server
}

// TestLoginContract runs the contract against the fake, scripted per case.
func TestLoginContract(t *testing.T) {
	LoginContract(t, func(_ *testing.T, c LoginCase) LoginSetup {
		l := Login{Agent: "codex"}
		s := LoginSetup{Process: launch.Process{Agent: "codex"}, Server: "wiki", WantArgv: []string{"codex", "login"}}
		scripted := scriptedLogin{Login: l}
		switch c {
		case CaseLoggedOut:
			scripted.status = &agent.LoginProblem{Agent: "codex", Login: l.LoginArgv("")}
		case CaseServerLogin:
			s.WantArgv = []string{"codex", "mcp", "login", "wiki"}
			scripted.server = &agent.LoginProblem{Agent: "codex", Server: "wiki", Login: l.LoginArgv("wiki")}
		}
		s.Login = scripted
		return s
	})
}

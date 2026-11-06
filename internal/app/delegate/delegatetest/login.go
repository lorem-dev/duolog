package delegatetest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// Login is a delegate.Login for one agent that checks nothing: every login
// passes, and the commands are the agent's own words.
type Login struct{ Agent string }

// Status passes: the fake agent is logged in.
func (Login) Status(context.Context, launch.Process) *agent.LoginProblem { return nil }

// Server passes: no server needs a login.
func (Login) Server(context.Context, launch.Process, string) *agent.LoginProblem { return nil }

// LoginArgv is the agent's login command, or its server login command.
func (l Login) LoginArgv(server string) []string {
	if server == "" {
		return []string{l.Agent, "login"}
	}
	return []string{l.Agent, "mcp", "login", server}
}

// declineLogins is Deps.RunLogin of the fakes: no login ever succeeds.
func declineLogins(context.Context, []string, time.Duration) agent.LoginResult {
	return agent.Declined
}

// LoginCase names one case of LoginContract.
type LoginCase string

// The cases of LoginContract.
const (
	CaseLoggedIn    LoginCase = "logged in gives no problem"
	CaseLoggedOut   LoginCase = "logged out gives a problem with the agent's login command"
	CaseServerLogin LoginCase = "a server that needs a login gives its own command"
)

// LoginPort is the shape of a delegate.Login that the contract needs, named
// here so that an agent's tests need not import the app package.
type LoginPort interface {
	Status(ctx context.Context, p launch.Process) *agent.LoginProblem
	Server(ctx context.Context, p launch.Process, name string) *agent.LoginProblem
	LoginArgv(server string) []string
}

// LoginSetup is one case, ready to run: the port, the worker's process, the
// server to ask about and the command a problem must carry.
type LoginSetup struct {
	Login    LoginPort
	Process  launch.Process
	Server   string
	WantArgv []string
}

// LoginContract runs the cases every Login meets. The factory prepares each
// case, with the fake agent binary where a process starts.
func LoginContract(t *testing.T, setup func(t *testing.T, c LoginCase) LoginSetup) {
	t.Helper()
	t.Run(string(CaseLoggedIn), func(t *testing.T) {
		s := setup(t, CaseLoggedIn)
		if p := s.Login.Status(context.Background(), s.Process); p != nil {
			t.Fatalf("problem %+v for a logged-in agent", p)
		}
	})
	t.Run(string(CaseLoggedOut), func(t *testing.T) {
		s := setup(t, CaseLoggedOut)
		p := s.Login.Status(context.Background(), s.Process)
		if p == nil || p.Server != "" || p.Agent == "" || !slices.Equal(p.Login, s.WantArgv) || !slices.Equal(s.Login.LoginArgv(""), s.WantArgv) {
			t.Fatalf("problem %+v; want one for the agent with login %q", p, s.WantArgv)
		}
	})
	t.Run(string(CaseServerLogin), func(t *testing.T) {
		s := setup(t, CaseServerLogin)
		p := s.Login.Server(context.Background(), s.Process, s.Server)
		if p == nil || p.Server != s.Server || !slices.Equal(p.Login, s.WantArgv) || !slices.Equal(s.Login.LoginArgv(s.Server), s.WantArgv) {
			t.Fatalf("problem %+v; want one for %q with login %q", p, s.Server, s.WantArgv)
		}
	})
}

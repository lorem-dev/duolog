package claude

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// Login checks Claude's logins and gives the commands that make them. A check
// of the agent runs the worker's own process; a check of a server runs
// `claude mcp get`, which connects to the server without a model call.
type Login struct{}

// Status reports a worker whose Claude is not logged in. No answer, a missing
// binary or a killed check is not a failed login.
func (l Login) Status(ctx context.Context, p launch.Process) *agent.LoginProblem {
	argv, env := kit.AgentArgv(p, "claude", "auth", "status", "--json")
	// The output decides; a failed run leaves it empty and is treated as no answer.
	out, _ := kit.Output(ctx, argv, env)
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal(out, &status) != nil || status.LoggedIn == nil || *status.LoggedIn {
		return nil
	}
	return &agent.LoginProblem{Agent: "claude", Reason: "claude auth status says it is not logged in", Login: l.LoginArgv("")}
}

// Server reports an OAuth server that Claude has no login for. Claude's own
// state is read in duolog's environment, as `claude mcp get` does.
func (l Login) Server(ctx context.Context, _ launch.Process, name string) *agent.LoginProblem {
	out, err := kit.Output(ctx, []string{"claude", "mcp", "get", name}, nil)
	if err != nil || !strings.Contains(string(out), "Needs authentication") {
		return nil
	}
	return &agent.LoginProblem{Agent: "claude", Server: name, Reason: "claude has no login for it", Login: l.LoginArgv(name)}
}

// LoginArgv is the command that logs in to Claude, or to one of its servers.
func (Login) LoginArgv(server string) []string {
	if server == "" {
		return []string{"claude", "auth", "login"}
	}
	return []string{"claude", "mcp", "login", server}
}

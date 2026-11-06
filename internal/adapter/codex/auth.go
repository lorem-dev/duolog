package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/launch"
)

// Login checks Codex's logins and gives the commands that make them. A check
// of the agent runs the worker's own process; a check of a server reads the
// login state that `codex mcp list --json` reports.
type Login struct{}

// Status reports a worker whose Codex is not logged in. `codex login status`
// exits 0 only when logged in; no answer, a missing binary or a killed check
// is not a failed login.
func (l Login) Status(ctx context.Context, p launch.Process) *agent.LoginProblem {
	argv, env := kit.AgentArgv(p, "codex", "login", "status")
	_, err := kit.Output(ctx, argv, env)
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() <= 0 {
		return nil
	}
	return &agent.LoginProblem{Agent: "codex", Reason: "codex login status says it is not logged in", Login: l.LoginArgv("")}
}

// Server reports an OAuth server that Codex has no login for.
func (l Login) Server(ctx context.Context, _ launch.Process, name string) *agent.LoginProblem {
	out, err := kit.Output(ctx, []string{"codex", "mcp", "list", "--json"}, nil)
	var list []struct {
		Name       string `json:"name"`
		AuthStatus string `json:"auth_status"`
	}
	if err != nil || json.Unmarshal(out, &list) != nil {
		return nil
	}
	for _, s := range list {
		if s.Name == name && s.AuthStatus == "not_logged_in" {
			return &agent.LoginProblem{Agent: "codex", Server: name, Reason: "codex has no OAuth login for it", Login: l.LoginArgv(name)}
		}
	}
	return nil
}

// LoginArgv is the command that logs in to Codex, or to one of its servers.
func (Login) LoginArgv(server string) []string {
	if server == "" {
		return []string{"codex", "login"}
	}
	return []string{"codex", "mcp", "login", server}
}

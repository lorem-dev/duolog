package agent

import (
	"regexp"
	"strings"
)

// LoginProblem is one credential that stops a worker. It never holds a value.
type LoginProblem struct {
	// Agent is the worker's agent. Server is empty for the agent's own login.
	Agent, Server string
	// Static means the credential is fixed in config: there is nothing to log
	// in to, and Where names the config entry that holds it.
	Static bool
	Where  string
	// Reason is one short clause, such as "the server answered 401".
	Reason string
	// Login is the agent's own login command, which the agent's Login fills in.
	// It stays empty for a static credential.
	Login []string
}

// Key names the problem in the lead's session record of declined logins.
func (p LoginProblem) Key() string {
	if p.Server == "" {
		return "agent:" + p.Agent
	}
	return p.Agent + ":mcp:" + p.Server
}

// Need is what the lead gets in needs when the task goes back to it.
func (p LoginProblem) Need() string {
	if p.Server == "" {
		return "auth:" + p.Agent
	}
	return "mcp:" + p.Server
}

// AuthNeed is the need for a login the lead must have the person run.
func (p LoginProblem) AuthNeed() string {
	if p.Server == "" {
		return "auth:" + p.Agent
	}
	return "auth:mcp:" + p.Server
}

// Subject names the agent or the server in a line for the lead.
func (p LoginProblem) Subject() string {
	if p.Server == "" {
		return p.Agent + " login"
	}
	return "mcp:" + p.Server + " in " + p.Agent
}

// LoginArgv is the agent's own login command, or nil for a static credential.
func (p LoginProblem) LoginArgv() []string {
	if p.Static {
		return nil
	}
	return p.Login
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

// Command is the login as one line for the lead to show the person. The
// person runs it in a shell, so every word other than plain characters is
// quoted: a server name comes from a worker's needs.
func (p LoginProblem) Command() string {
	argv := append([]string(nil), p.LoginArgv()...)
	for i, a := range argv {
		if a == "" || !plainWord.MatchString(a) {
			argv[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(argv, " ")
}

// LoginResult says how a login ended.
type LoginResult int

const (
	// LoggedIn means the command succeeded.
	LoggedIn LoginResult = iota
	// NeedsTerminal means the command needs a terminal that duolog lacks.
	NeedsTerminal
	// Declined covers a closed, refused or failed login.
	Declined
	// TimedOut means the person did not finish within the timeout, perhaps
	// because no browser opened; it may work from a terminal.
	TimedOut
)

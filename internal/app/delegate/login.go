package delegate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lorem-dev/duolog/internal/capability"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// Actions for capabilities.on_auth_failure.
const (
	AuthLoginThenLead = "login_then_lead"
	AuthLead          = "lead"
	AuthFail          = "fail"
)

// AuthWorkerLine tells a worker with MCP needs how to report a rejected login.
const AuthWorkerLine = "If an MCP server rejects its credentials (401, 403, an invalid or expired token, or a login it needs), stop and answer done false with auth:mcp:<server> in needs."

// cacheTTL keeps a passed check for a few minutes, so that a run of
// delegations does not ask the agent or the server every time.
const cacheTTL = 5 * time.Minute

// Problem is one credential that stops a worker, and LoginResult says how a
// login ended; core/agent holds both.
type (
	Problem     = coreagent.LoginProblem
	LoginResult = coreagent.LoginResult
)

// How a login ended.
const (
	LoggedIn      = coreagent.LoggedIn
	NeedsTerminal = coreagent.NeedsTerminal
	Declined      = coreagent.Declined
	TimedOut      = coreagent.TimedOut
)

// Login checks one agent's logins. Status runs the worker's own process p;
// Server checks an OAuth server that no file defines; LoginArgv is the command
// that makes the login, for the agent or for a server (the name "" is the
// agent). A problem a Login returns names its agent and carries its command.
type Login interface {
	Status(ctx context.Context, p corelaunch.Process) *Problem
	Server(ctx context.Context, p corelaunch.Process, name string) *Problem
	LoginArgv(server string) []string
}

// ServerOAuth checks a static HTTP server with the credentials the worker would
// get. The problem it returns names no agent or server.
type ServerOAuth interface {
	Check(ctx context.Context, def capability.Server) *Problem
}

// Checker is what Run needs to check logins; tests replace it.
type Checker interface {
	// Agent reports a worker agent that is not logged in.
	Agent(ctx context.Context, agent string) *Problem
	// Server reports a server the worker cannot use. def is its definition
	// from a config file, or nil for a server no file lists.
	Server(ctx context.Context, agent, name string, def *capability.Server) *Problem
	// Login runs the agent's login for p once, within timeout.
	Login(ctx context.Context, p Problem, timeout time.Duration) LoginResult
	// Forget drops a passed check of p, so that the next check asks again.
	Forget(p Problem)
}

// NewChecker returns the checker of a worker that runs as process p, over the
// logins of d. Its agent check is the worker's own: p's argv prefix, settings
// and exactly p's environment, so a login that only duolog's environment has
// cannot pass it. A passed check counts for p's route alone, by its cache key,
// never for the agent. cacheDir is .duolog/cache, or empty for none.
func NewChecker(d Deps, cacheDir string, p workerenv.Process) Checker {
	return &loginChecker{d: d, dir: cacheDir, proc: p, now: time.Now}
}

type loginChecker struct {
	d    Deps
	dir  string
	proc workerenv.Process
	now  func() time.Time
	mu   sync.Mutex
}

// login is the Login of agent, or nil.
func (d Deps) login(agent string) Login {
	return d.Agents[agent].Login
}

// withLogin gives p the login command of its agent, when it has none.
func (d Deps) withLogin(p Problem) Problem {
	if p.Static || len(p.Login) > 0 {
		return p
	}
	if l := d.login(p.Agent); l != nil {
		p.Login = l.LoginArgv(p.Server)
	}
	return p
}

func (c *loginChecker) Agent(ctx context.Context, agent string) *Problem {
	p := Problem{Agent: agent}
	if c.passed(p) {
		return nil
	}
	l := c.d.login(agent)
	if l == nil {
		return nil
	}
	proc := corelaunch.Process{}
	if c.proc.Agent == agent {
		proc = c.proc
	}
	if problem := l.Status(ctx, proc); problem != nil {
		return c.fill(problem, agent, "")
	}
	c.pass(p)
	return nil
}

func (c *loginChecker) Server(ctx context.Context, agent, name string, def *capability.Server) *Problem {
	p := Problem{Agent: agent, Server: name}
	if c.passed(p) {
		return nil
	}
	var problem *Problem
	switch {
	case def != nil && def.Transport == "http" && def.Static():
		if c.d.OAuth != nil {
			problem = c.d.OAuth.Check(ctx, *def)
		}
	case def != nil && def.Transport != "http":
		// A stdio server would have to be started to check it.
		return nil
	default:
		if l := c.d.login(agent); l != nil {
			problem = l.Server(ctx, c.proc, name)
		}
	}
	if problem == nil {
		c.pass(p)
		return nil
	}
	return c.fill(problem, agent, name)
}

// fill names the agent and server of a problem that a check returned, and
// gives it the login command of its agent.
func (c *loginChecker) fill(problem *Problem, agent, server string) *Problem {
	problem.Agent, problem.Server = agent, server
	filled := c.d.withLogin(*problem)
	return &filled
}

func (c *loginChecker) Login(ctx context.Context, p Problem, timeout time.Duration) LoginResult {
	p = c.d.withLogin(p)
	argv := p.LoginArgv()
	if argv == nil || c.d.RunLogin == nil {
		return Declined
	}
	result := c.d.RunLogin(ctx, argv, timeout)
	if result == LoggedIn {
		c.Forget(p)
	}
	return result
}

// The cache file maps a problem key to the time its check passed.
func (c *loginChecker) path() string { return filepath.Join(c.dir, "auth.json") }

func (c *loginChecker) load() map[string]time.Time {
	m := map[string]time.Time{}
	if c.dir == "" {
		return m
	}
	data, err := os.ReadFile(c.path())
	if err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func (c *loginChecker) save(m map[string]time.Time) {
	if c.dir == "" || os.MkdirAll(c.dir, 0755) != nil {
		return
	}
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	f, err := os.CreateTemp(c.dir, ".auth-*")
	if err != nil {
		return
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil && cerr == nil {
		_ = store.Rename(f.Name(), c.path())
	}
	_ = os.Remove(f.Name())
}

// cacheKey names a passed check in the cache: an agent check of the worker's
// process by its route as well, so a pass of another route or of duolog's
// own environment never stands for it.
func (c *loginChecker) cacheKey(p Problem) string {
	if p.Server == "" && c.proc.Agent == p.Agent && p.Agent != "" {
		return p.Key() + "@" + c.proc.CacheKey
	}
	return p.Key()
}

func (c *loginChecker) passed(p Problem) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.load()[c.cacheKey(p)]
	return ok && c.now().Sub(at) < cacheTTL && !at.After(c.now())
}

func (c *loginChecker) pass(p Problem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.load()
	m[c.cacheKey(p)] = c.now()
	c.save(m)
}

func (c *loginChecker) Forget(p Problem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.load()
	delete(m, p.Key())
	delete(m, c.cacheKey(p))
	c.save(m)
}

// fromNeeds turns the auth: entries a worker put in needs into problems.
// A worker writes auth:mcp:<server> for a server that rejected its
// credentials, or auth:<agent>.
func fromNeeds(agent string, needs []string) []Problem {
	var out []Problem
	for _, need := range needs {
		rest, ok := strings.CutPrefix(strings.TrimSpace(need), "auth:")
		if !ok {
			continue
		}
		if server, ok := strings.CutPrefix(rest, "mcp:"); ok && server != "" {
			out = append(out, Problem{Agent: agent, Server: server, Reason: "the worker says the server rejected its credentials"})
		} else {
			out = append(out, Problem{Agent: agent, Reason: "the worker says its login failed"})
		}
	}
	return out
}

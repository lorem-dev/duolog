package delegate

import (
	"context"
	"fmt"
	"time"

	"github.com/lorem-dev/duolog/internal/app/launch"
	leadio "github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/quota"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// Worker runs one agent's workers. Launch gives the words the account gate
// admits in the worker's process; Run refuses a process built with others.
type Worker interface {
	Run(ctx context.Context, s run.Spec) (run.Result, error)
	Launch(s run.Spec) []string
}

// ModelResolver turns a route's target into the concrete model of its agent.
// cacheDir is the folder of the agent's catalog cache, or "" for none.
type ModelResolver interface {
	Resolve(ctx context.Context, cfg *config.Config, cacheDir string, t config.Target) (string, error)
}

// Agent is what Run reaches of one agent. Later ports add their fields here.
// A nil Trust fails closed: an agent with no Trust reads as untrusted. So
// does a nil Quota: the account gate reads through the same port, bound to
// the account's identity, so an agent without one is never read, its
// verdict is paid, and it gets no paid pass. A nil Caps lists nothing: its
// inventory is unknown, and it defines, lends and takes no MCP server.
type Agent struct {
	Worker Worker
	Models ModelResolver
	Login  Login
	Trust  Trust
	// Quota only shows that the agent has a reader. wiring builds it without
	// an identity sink, so it must never read: a read through it would not
	// record the account's identity, and a switched login would go unseen.
	Quota quota.Reader
	Caps  Capabilities
}

// Capabilities reads one agent's skills, plugins and MCP servers, and takes
// MCP servers of another agent for one of its workers. The bridge from agent A
// to agent B is B.Import(A.Servers(...)). environ and worker are the
// environment of the agent process whose folders are read: duolog's own for
// the lead, the one the worker gets for a worker.
type Capabilities interface {
	// Scan lists what the agent has; an unknown inventory claims nothing.
	Scan(ctx context.Context, root string, environ []string) (caps.Inventory, error)
	// Servers reads the MCP servers the agent's config files define for the
	// project at root, with the agent's own precedence.
	Servers(root string, environ []string) map[string]caps.Server
	// Import translates servers of another agent into this agent's format for
	// one worker run; a server that cannot be bridged is in failed, with why.
	// tools limits the tools of a server.
	Import(root string, servers map[string]caps.Server, tools map[string][]string, worker []string) (m *run.MCP, failed map[string]string)
	// Refusal says why s cannot go to a worker of this agent from its
	// definition alone, or "" when it can.
	Refusal(root string, s caps.Server, worker []string) string
	// Limit applies capabilities.mcp_tools to the servers named that a worker
	// of this agent has itself.
	Limit(root string, names []string, tools map[string][]string, worker []string) (codexArgs, allowedTools []string)
}

// checker returns the checker of the worker process p: the real one over the
// logins of d, or the one NewChecker of d gives when a test replaced it.
func (d Deps) checker(cacheDir string, p corelaunch.Process) Checker {
	if d.NewChecker != nil {
		return d.NewChecker(cacheDir, p)
	}
	return NewChecker(d, cacheDir, p)
}

// resolveModel gives the concrete model of t through its agent's resolver.
func (d Deps) resolveModel(ctx context.Context, cfg *config.Config, cacheDir string, t config.Target) (string, error) {
	a, ok := d.Agents[t.Agent]
	if !ok || a.Models == nil {
		return "", fmt.Errorf("unknown agent %q", t.Agent)
	}
	return a.Models.Resolve(ctx, cfg, cacheDir, t)
}

// Runner runs one delegation. Run, bound to its Deps, is the real one.
type Runner func(ctx context.Context, r Request) (Outcome, error)

// Request is one delegation: the project's config and state folder, the role,
// the task file, the reusable thread and the options of the caller.
type Request struct {
	Config                 *config.Config
	StateDir               string // the project's .duolog folder
	Role, TaskPath, Thread string
	Options                Options
}

// Trust reads what an agent's settings and approvals say about duolog's
// hooks. Hooks and Folder read the state of a process whose environment is
// environ; an environment without a home folder is an error that wraps
// agent.ErrNoHome. GateProblem words why the gate cannot be shown to run
// from that state, and HeldGateProblem why a launch that only the gate keeps
// read-only cannot be shown to run it; both are empty when it can.
type Trust interface {
	Hooks(root string, environ []string) (coreagent.TrustState, error)
	Folder(root string, environ []string) (bool, error)
	GateProblem(s coreagent.TrustState, folder bool) string
	HeldGateProblem(root string, environ []string, gate string) string
}

// Deps is what Run reaches outside the package: the worker's process, the
// model lookup, the lead's transcript, the account gate and the path check;
// the capability scan and the MCP bridge are each agent's Caps. The wiring package fills every field with the real
// function; app tests fill them with fakes.
type Deps struct {
	// Agents holds the ports of each registered agent, by name.
	Agents map[string]Agent
	// Lead holds each agent's Lead, keyed by agent name; the transcript of
	// the lead is read through it.
	Lead map[string]leadio.Lead
	// NewChecker, when set, replaces the checker that Run builds over the
	// logins of Agents; a test sets it to script the answers.
	NewChecker func(cacheDir string, p corelaunch.Process) Checker
	// OAuth checks a static HTTP server with the credentials the worker gets.
	OAuth ServerOAuth
	// RunLogin runs an agent's login command once, within the timeout, and says
	// how it ended. A timeout that is not positive means the default.
	RunLogin func(ctx context.Context, argv []string, timeout time.Duration) LoginResult
	// Build makes a worker's process: workerenv.Build, the one Build.
	Build launch.Build
	// DroppedKeys lists, per agent, the route names that the agent's workers
	// no longer get: workerenv.DroppedKeys.
	DroppedKeys launch.DroppedKeys
	// PaidGate opens the account admission gate of one delegation: the user's
	// account state, reads of the quota each built process sees, and the
	// settings layers each build reads.
	PaidGate func(cfg *config.Config) (*paidruns.Gate, error)
	// GateBinary finds the duolog a worker's hooks run on a PATH=... pair.
	GateBinary func(pathEnv string) (string, error)
	// Canonical spells a local path so that two spellings of one folder
	// compare equal.
	Canonical func(path string) string
}

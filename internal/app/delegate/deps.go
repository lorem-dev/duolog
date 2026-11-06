package delegate

import (
	"context"
	"fmt"

	leadio "github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/auth"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/workerenv"
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
type Agent struct {
	Worker Worker
	Models ModelResolver
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

// Deps is what Run reaches outside the package: the worker's process, the
// model lookup, the lead's transcript, the capability scan, the account gate
// and the path check. The wiring package fills every field with the real
// function; app tests fill them with fakes.
type Deps struct {
	// Agents holds the ports of each registered agent, by name.
	Agents map[string]Agent
	// Lead holds each agent's Lead, keyed by agent name; the transcript of
	// the lead is read through it.
	Lead             map[string]leadio.Lead
	ScanCapabilities func(ctx context.Context, agent, root string, environ []string) (capability.Inventory, error)
	BridgeMCP        func(from, to, root string, names []string, tools map[string][]string, workerEnv []string) (*run.MCP, map[string]string)
	// NewChecker checks a worker's logins on the worker's own built process.
	NewChecker func(cacheDir string, p workerenv.Process) auth.Checker
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

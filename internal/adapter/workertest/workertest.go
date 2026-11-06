// Package workertest gives tests of the app packages the real Worker of an
// agent, without the registry: its run, its launch words, its argv and its
// parse of a limit message. It imports no app package, so any test may use it.
// Only tests import it.
package workertest

import (
	"context"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// Worker is the real Worker of one agent.
type Worker interface {
	Run(ctx context.Context, s run.Spec) (run.Result, error)
	Launch(s run.Spec) []string
	Argv(s run.Spec) []string
}

// For returns the Worker of the named agent: Codex's for "codex", Claude's for
// any other name.
func For(agent string) Worker {
	if agent == "codex" {
		return codex.Worker{}
	}
	return claude.Worker{}
}

// Login is the real Login of one agent.
type Login interface {
	Status(ctx context.Context, p launch.Process) *agent.LoginProblem
	Server(ctx context.Context, p launch.Process, name string) *agent.LoginProblem
	LoginArgv(server string) []string
}

// LoginFor returns the Login of the named agent: Codex's for "codex", Claude's
// for any other name.
func LoginFor(name string) Login {
	if name == "codex" {
		return codex.Login{}
	}
	return claude.Login{}
}

// Argv is the argv the Worker of s's agent gives s.
func Argv(s run.Spec) []string { return For(s.Route.Agent).Argv(s) }

// ResetTime parses a limit message of the named agent, with a fallback when it
// names no reset.
func ResetTime(agent, text string, now time.Time) time.Time {
	if agent == "codex" {
		return codex.ResetTime(text, now)
	}
	return claude.ResetTime(text, now)
}

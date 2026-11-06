// Package wiring is the one place that knows every agent.
package wiring

import (
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/hook"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/core/agent"
)

// Agent is one registered agent. Later ports add their fields here.
type Agent struct {
	Facts  agent.Descriptor
	Lead   lead.Lead
	Events hook.Events
	// Worker runs this agent's workers.
	Worker delegate.Worker
	// ResetTime parses the time a limit message of this agent says the quota
	// returns, or gives a fallback; never the zero time.
	ResetTime func(text string, now time.Time) time.Time
	// Models resolves this agent's model names.
	Models delegate.ModelResolver
}

// Agents returns the registered agents in order: claude, then codex.
func Agents() []Agent {
	return []Agent{
		{Facts: claude.Facts, Lead: claude.Lead{}, Events: claude.Events{}, Worker: claude.Worker{}, ResetTime: claude.ResetTime, Models: claude.Resolver{}},
		{Facts: codex.Facts, Lead: codex.Lead{}, Events: codex.Events{}, Worker: codex.Worker{}, ResetTime: codex.ResetTime, Models: codex.Resolver{}},
	}
}

// HookEnv fills the Events and Lead of env.Agent. An agent that is not
// registered gets none, and every hook of it does nothing.
func HookEnv(env hook.Env) hook.Env {
	if a, ok := Find(env.Agent); ok {
		env.Events, env.Lead = a.Events, a.Lead
	}
	return env
}

// Find returns the registered agent called name.
func Find(name string) (Agent, bool) {
	for _, a := range Agents() {
		if a.Facts.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// Names returns the names of the registered agents, in order.
func Names() []string {
	all := Agents()
	names := make([]string, len(all))
	for i, a := range all {
		names[i] = a.Facts.Name
	}
	return names
}

// delegateAgents builds the ports of delegate.Deps.Agents from the registry.
func delegateAgents() map[string]delegate.Agent {
	out := map[string]delegate.Agent{}
	for _, a := range Agents() {
		out[a.Facts.Name] = delegate.Agent{Worker: a.Worker, Models: a.Models}
	}
	return out
}

// peerModels builds the model resolvers of peer.Deps from the registry.
func peerModels() map[string]peer.ModelResolver {
	out := map[string]peer.ModelResolver{}
	for _, a := range Agents() {
		out[a.Facts.Name] = a.Models
	}
	return out
}

// resetTime parses a limit message of the named agent; an unknown agent gets
// no parse.
func resetTime(name, text string, now time.Time) time.Time {
	for _, a := range Agents() {
		if a.Facts.Name == name {
			return a.ResetTime(text, now)
		}
	}
	return now.Add(time.Hour)
}

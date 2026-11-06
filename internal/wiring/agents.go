// Package wiring is the one place that knows every agent.
package wiring

import (
	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/hook"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/core/agent"
)

// Agent is one registered agent. Later ports add their fields here.
type Agent struct {
	Facts  agent.Descriptor
	Lead   lead.Lead
	Events hook.Events
}

// Agents returns the registered agents in order: claude, then codex.
func Agents() []Agent {
	return []Agent{
		{Facts: claude.Facts, Lead: claude.Lead{}, Events: claude.Events{}},
		{Facts: codex.Facts, Lead: codex.Lead{}, Events: codex.Events{}},
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

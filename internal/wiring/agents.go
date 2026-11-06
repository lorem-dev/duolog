// Package wiring is the one place that knows every agent.
package wiring

import (
	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/core/agent"
)

// Agent is one registered agent. Later ports add their fields here.
type Agent struct {
	Facts agent.Descriptor
	Lead  lead.Lead
}

// Agents returns the registered agents in order: claude, then codex.
func Agents() []Agent {
	return []Agent{
		{Facts: claude.Facts, Lead: claude.Lead{}},
		{Facts: codex.Facts, Lead: codex.Lead{}},
	}
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

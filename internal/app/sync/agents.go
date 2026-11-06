// Package agentsync converts agent files and skills between the agents'
// formats. The source side holds the files that people edit; every other
// agent's format is generated from it.
package agentsync

import (
	"sort"

	"github.com/lorem-dev/duolog/internal/core/agentfile"
)

// Agent is an agent file in the form of the file that will be written.
type Agent = agentfile.Agent

// AgentOpts holds the settings for one conversion.
type AgentOpts = agentfile.AgentOpts

// Dirs names where an agent keeps its files.
type Dirs = agentfile.Dirs

// Format is what sync needs of one agent: its folders, its agent files and its
// skills. Each agent implements it in its adapter package.
type Format = agentfile.Format

// Formats holds the format of each agent, keyed by the agent's name.
type Formats map[string]Format

// names returns the names of the formats in order.
func (f Formats) names() []string {
	names := make([]string, 0, len(f))
	for name := range f {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

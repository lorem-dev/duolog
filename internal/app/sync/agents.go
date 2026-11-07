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

// SkillsFolder is implemented by a Format whose project skills folder
// agents.<name>.skills_dir sets: it returns the format with that folder.
type SkillsFolder interface {
	WithSkillsDir(dir string) Format
}

// Configured returns the formats with the project skills folder that dirs
// names for each agent (agents.<name>.skills_dir of the project being synced).
// A format without a SkillsFolder, or an agent that dirs does not name, stays
// as it is.
func (f Formats) Configured(dirs map[string]string) Formats {
	out := make(Formats, len(f))
	for name, format := range f {
		if s, ok := format.(SkillsFolder); ok {
			if dir := dirs[name]; dir != "" {
				format = s.WithSkillsDir(dir)
			}
		}
		out[name] = format
	}
	return out
}

// names returns the names of the formats in order.
func (f Formats) names() []string {
	names := make([]string, 0, len(f))
	for name := range f {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

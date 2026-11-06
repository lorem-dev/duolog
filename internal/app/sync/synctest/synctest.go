// Package synctest holds the fake sync format, the contract that every format
// passes, and the real formats for tests of the packages that call sync. It
// does not import app/sync, so that tests inside that package can use it.
package synctest

import (
	"errors"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/core/agentfile"
)

// Real returns the formats of the two real agents, keyed by name. It is
// assignable to agentsync.Formats.
func Real() map[string]agentfile.Format {
	return map[string]agentfile.Format{"claude": claude.Format{}, "codex": codex.Format{}}
}

// Fake is a format that records each call and returns scripted results.
type Fake struct {
	// Folders is what Dirs returns.
	Folders agentfile.Dirs
	// Parsed and ParseErr are what ParseAgent returns.
	Parsed   agentfile.Agent
	ParseErr error
	// Rendered is what RenderAgent returns for every agent.
	Rendered []byte
	// SkillOut and SkillWarnings are what Skill returns.
	SkillOut      []byte
	SkillWarnings []string
	// Valid is the content that Check accepts; any other content is refused.
	Valid []byte

	// Calls lists the calls in order: "parse <path>", "render <name>",
	// "skill", "check <path>".
	Calls []string
}

var _ agentfile.Format = (*Fake)(nil)

// Dirs returns Folders.
func (f *Fake) Dirs() agentfile.Dirs { return f.Folders }

// ParseAgent records the call and returns Parsed.
func (f *Fake) ParseAgent(path string, _ []byte, _ agentfile.AgentOpts) (agentfile.Agent, error) {
	f.Calls = append(f.Calls, "parse "+path)
	return f.Parsed, f.ParseErr
}

// RenderAgent records the call and returns Rendered.
func (f *Fake) RenderAgent(a agentfile.Agent) ([]byte, error) {
	f.Calls = append(f.Calls, "render "+a.Name)
	if a.Name == "" {
		return nil, errors.New("agent needs a name and a description")
	}
	return f.Rendered, nil
}

// Skill records the call and returns SkillOut and SkillWarnings.
func (f *Fake) Skill([]byte, []string) ([]byte, []string, error) {
	f.Calls = append(f.Calls, "skill")
	return f.SkillOut, f.SkillWarnings, nil
}

// Check records the call and accepts only Valid.
func (f *Fake) Check(path string, data []byte) error {
	f.Calls = append(f.Calls, "check "+path)
	if string(data) != string(f.Valid) {
		return errors.New("does not parse")
	}
	return nil
}

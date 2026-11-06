package claude

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/agentfile"
	"github.com/lorem-dev/duolog/internal/core/agentmodel"
)

// claudeKeys are the frontmatter keys of a Claude agent file that have a
// counterpart in a Codex agent file.
var claudeKeys = []string{"name", "description", "model", "effort"}

// Format is the sync format of Claude: Markdown agent files with a YAML header.
type Format struct{}

var _ agentfile.Format = Format{}

// Dirs returns the folders of Claude's project agents and skills.
func (Format) Dirs() agentfile.Dirs {
	return agentfile.Dirs{Agents: ".claude/agents", Skills: ".claude/skills", Ext: ".md"}
}

// ParseAgent reads a Claude Markdown agent and returns it in the form of the
// Codex file.
func (Format) ParseAgent(path string, src []byte, o agentfile.AgentOpts) (agentfile.Agent, error) {
	a, err := parseAgent(src, o)
	if err != nil {
		return agentfile.Agent{}, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

func parseAgent(src []byte, o agentfile.AgentOpts) (agentfile.Agent, error) {
	if o.To != "codex" {
		return agentfile.Agent{}, fmt.Errorf("no conversion from a Claude agent file to %q", o.To)
	}
	header, body, err := kit.SplitFrontmatter(src)
	if err != nil {
		return agentfile.Agent{}, err
	}
	fields, err := kit.ParseHeader(header)
	if err != nil {
		return agentfile.Agent{}, err
	}
	if err := agentfile.CheckKeys(agentfile.SortedKeys(fields), claudeKeys, o.Config.DropKeys); err != nil {
		return agentfile.Agent{}, err
	}
	name, desc, model, effort, err := agentfile.CommonFields(fields, "model", "effort")
	if err != nil {
		return agentfile.Agent{}, err
	}
	codexName, codexModel, codexEffort, err := agentmodel.ToCodex(o, name, model, effort)
	if err != nil {
		return agentfile.Agent{}, err
	}
	return agentfile.Agent{Name: codexName, Description: desc, Model: codexModel, Effort: codexEffort, Body: body, Original: name, Resolved: codexModel}, nil
}

// RenderAgent writes a Claude Markdown file and reads it back to check it.
func (Format) RenderAgent(a agentfile.Agent) ([]byte, error) {
	if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Description) == "" {
		return nil, errors.New("agent needs a name and a description")
	}
	header, err := kit.RenderHeader(a)
	if err != nil {
		return nil, fmt.Errorf("render YAML header: %w", err)
	}
	out := []byte("---\n" + agentfile.GeneratedComment + "\n" + string(header) + "---\n" + a.Body)

	h, body, err := kit.SplitFrontmatter(out)
	if err != nil {
		return nil, fmt.Errorf("rendered Markdown does not parse: %w", err)
	}
	fields, err := kit.ParseHeader(h)
	if err != nil {
		return nil, fmt.Errorf("rendered Markdown does not parse: %w", err)
	}
	got := agentfile.Agent{Name: fields["name"], Description: fields["description"], Model: fields["model"], Effort: fields["effort"], Body: body}
	if want := a.Content(); got != want {
		return nil, fmt.Errorf("rendered Markdown reads back differently: got %#v, want %#v", got, want)
	}
	return out, nil
}

// Skill rewrites the frontmatter of a SKILL.md for Codex.
func (Format) Skill(src []byte, drop []string) ([]byte, []string, error) {
	return kit.ConvertSkill(src, drop, "disable-model-invocation", "disable-model-invocation is not honoured by Codex; the model may still pick this skill")
}

// Check reports whether data has a YAML header that parses.
func (Format) Check(_ string, data []byte) error {
	header, _, err := kit.SplitFrontmatter(data)
	if err != nil {
		return err
	}
	_, err = kit.ParseHeader(header)
	return err
}

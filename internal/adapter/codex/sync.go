package codex

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/agentfile"
	"github.com/lorem-dev/duolog/internal/core/agentmodel"
)

// codexKeys are the keys of a Codex agent file that have a counterpart in a
// Claude agent file.
var codexKeys = []string{"name", "description", "model", "model_reasoning_effort", "developer_instructions"}

// Format is the sync format of Codex: TOML agent files.
type Format struct {
	// SkillsDir is agents.codex.skills_dir: the project skills folder, with
	// slashes, below the project root. "" means DefaultSkillsDir.
	SkillsDir string
}

var _ agentfile.Format = Format{}

// WithSkillsDir returns f with the project skills folder dir, as
// agents.codex.skills_dir sets it.
func (f Format) WithSkillsDir(dir string) agentfile.Format {
	f.SkillsDir = dir
	return f
}

// Dirs returns the folders of Codex's project agents and skills.
func (f Format) Dirs() agentfile.Dirs {
	return agentfile.Dirs{Agents: ".codex/agents", Skills: SkillsDirOrDefault(f.SkillsDir), Ext: ".toml"}
}

// ParseAgent reads a Codex TOML agent and returns it in the form of the
// Claude file.
func (Format) ParseAgent(path string, src []byte, o agentfile.AgentOpts) (agentfile.Agent, error) {
	a, err := parseAgent(src, o)
	if err != nil {
		return agentfile.Agent{}, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

func parseAgent(src []byte, o agentfile.AgentOpts) (agentfile.Agent, error) {
	if o.To != "claude" {
		return agentfile.Agent{}, fmt.Errorf("no conversion from a Codex agent file to %q", o.To)
	}
	var raw map[string]any
	if err := toml.Unmarshal(kit.NormalizeNewlines(src), &raw); err != nil {
		return agentfile.Agent{}, fmt.Errorf("bad TOML: %w", err)
	}
	fields := map[string]string{}
	for key, value := range raw {
		if s, ok := value.(string); ok {
			fields[key] = s
		} else {
			fields[key] = agentfile.NonScalar
		}
	}
	if err := agentfile.CheckKeys(agentfile.SortedKeys(fields), codexKeys, o.Config.DropKeys); err != nil {
		return agentfile.Agent{}, err
	}
	name, desc, model, effort, err := agentfile.CommonFields(fields, "model", "model_reasoning_effort")
	if err != nil {
		return agentfile.Agent{}, err
	}
	body := fields["developer_instructions"]
	if body == agentfile.NonScalar {
		return agentfile.Agent{}, errors.New("developer_instructions must be a string")
	}
	model, effort, err = agentmodel.MapModel(o.Config, name, model, effort, "codex", o.Flags)
	if err != nil {
		return agentfile.Agent{}, err
	}
	return agentfile.Agent{Name: name, Description: desc, Model: model, Effort: effort, Body: body, Original: name}, nil
}

// RenderAgent writes a Codex TOML file and reads it back to check it.
func (Format) RenderAgent(a agentfile.Agent) ([]byte, error) {
	if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Description) == "" {
		return nil, errors.New("agent needs a name and a description")
	}
	var b strings.Builder
	b.WriteString(agentfile.GeneratedComment + "\n")
	line := func(key, value string) error {
		s, err := tomlBasic(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		b.WriteString(key + " = " + s + "\n")
		return nil
	}
	if err := line("name", a.Name); err != nil {
		return nil, err
	}
	if err := line("description", a.Description); err != nil {
		return nil, err
	}
	if a.Model != "" {
		if err := line("model", a.Model); err != nil {
			return nil, err
		}
	}
	if a.Effort != "" {
		if err := line("model_reasoning_effort", a.Effort); err != nil {
			return nil, err
		}
	}
	body, err := tomlMultiline(a.Body)
	if err != nil {
		return nil, fmt.Errorf("developer_instructions: %w", err)
	}
	b.WriteString("developer_instructions = " + body + "\n")
	out := []byte(b.String())

	var back struct {
		Name         string `toml:"name"`
		Description  string `toml:"description"`
		Model        string `toml:"model"`
		Effort       string `toml:"model_reasoning_effort"`
		Instructions string `toml:"developer_instructions"`
	}
	if err := toml.Unmarshal(out, &back); err != nil {
		return nil, fmt.Errorf("rendered TOML does not parse: %w", err)
	}
	got := agentfile.Agent{Name: back.Name, Description: back.Description, Model: back.Model, Effort: back.Effort, Body: back.Instructions}
	if want := a.Content(); got != want {
		return nil, fmt.Errorf("rendered TOML reads back differently: got %#v, want %#v", got, want)
	}
	return out, nil
}

// Skill rewrites the frontmatter of a SKILL.md for Claude. Claude honours
// every key Codex keeps, so nothing warns.
func (Format) Skill(src []byte, drop []string) ([]byte, []string, error) {
	return kit.ConvertSkill(src, drop, "", "")
}

// Check reports whether data parses as TOML.
func (Format) Check(_ string, data []byte) error {
	var v map[string]any
	return toml.Unmarshal(kit.NormalizeNewlines(data), &v)
}

// tomlBasic writes a single-line TOML basic string.
func tomlBasic(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errors.New("value is not valid UTF-8")
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		writeEscaped(&b, r, true)
	}
	b.WriteByte('"')
	return b.String(), nil
}

// tomlMultiline writes a multi-line TOML basic string. Every quote and
// backslash is escaped, so no body can close the string early. Spaces and tabs
// before a line break are escaped, so no output line ends in whitespace.
func tomlMultiline(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errors.New("value is not valid UTF-8")
	}
	var b strings.Builder
	b.WriteString("\"\"\"\n")
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == ' ' || r == '\t' {
			j := i
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			if j < len(runes) && runes[j] == '\n' {
				for ; i < j; i++ {
					if runes[i] == ' ' {
						b.WriteString("\\u0020")
					} else {
						b.WriteString(`\t`)
					}
				}
				i = j - 1
				continue
			}
		}
		if r == '\n' {
			b.WriteByte('\n')
			continue
		}
		writeEscaped(&b, r, false)
	}
	b.WriteString("\"\"\"")
	return b.String(), nil
}

func writeEscaped(b *strings.Builder, r rune, singleLine bool) {
	switch {
	case r == '"':
		b.WriteString(`\"`)
	case r == '\\':
		b.WriteString(`\\`)
	case r == '\t':
		b.WriteString(`\t`)
	case r == '\n' && singleLine:
		b.WriteString(`\n`)
	case r == '\r':
		b.WriteString(`\r`)
	case r < 0x20 || r == 0x7f:
		fmt.Fprintf(b, `\u%04X`, r)
	default:
		b.WriteRune(r)
	}
}

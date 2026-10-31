// Package agentsync converts agent files between Claude Markdown and Codex TOML.
package agentsync

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/models"
)

// Agent is an agent file in the form of the file that will be written.
// ParseClaudeAgent returns it in Codex form, ParseCodexAgent in Claude form.
type Agent struct {
	Name        string
	Description string
	Model       string
	Effort      string
	Body        string
}

// AgentOpts holds the settings for one conversion.
type AgentOpts struct {
	Config config.AgentSync
	// Flags maps a source alias to a target alias, from --model opus=terra.
	Flags map[string]string
	// Resolve turns a Codex alias into the concrete model written to the file.
	Resolve func(alias string) (string, error)
}

var (
	claudeKeys = []string{"name", "description", "model", "effort"}
	codexKeys  = []string{"name", "description", "model", "model_reasoning_effort", "developer_instructions"}
)

// ParseClaudeAgent reads a Claude Markdown agent and returns it in Codex form.
func ParseClaudeAgent(path string, src []byte, opts AgentOpts) (Agent, error) {
	a, err := parseClaude(src, opts)
	if err != nil {
		return Agent{}, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

func parseClaude(src []byte, opts AgentOpts) (Agent, error) {
	header, body, err := splitFrontmatter(src)
	if err != nil {
		return Agent{}, err
	}
	fields, err := parseHeader(header)
	if err != nil {
		return Agent{}, err
	}
	if err := checkKeys(sortedKeys(fields), claudeKeys, opts.Config.DropKeys); err != nil {
		return Agent{}, err
	}
	name, desc, model, effort, err := commonFields(fields, "model", "effort")
	if err != nil {
		return Agent{}, err
	}
	target, effort, err := MapModel(opts.Config, name, model, effort, "claude", opts.Flags)
	if err != nil {
		return Agent{}, err
	}
	if target != "" && !strings.HasPrefix(target, "gpt-") {
		if opts.Resolve == nil {
			return Agent{}, errors.New("no model resolver for Codex aliases")
		}
		if target, err = opts.Resolve(target); err != nil {
			return Agent{}, fmt.Errorf("resolve Codex model: %w", err)
		}
	}
	name, err = codexName(opts.Config.CodexName, name)
	if err != nil {
		return Agent{}, err
	}
	return Agent{Name: name, Description: desc, Model: target, Effort: effort, Body: body}, nil
}

// ParseCodexAgent reads a Codex TOML agent and returns it in Claude form.
func ParseCodexAgent(path string, src []byte, opts AgentOpts) (Agent, error) {
	a, err := parseCodex(src, opts)
	if err != nil {
		return Agent{}, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

func parseCodex(src []byte, opts AgentOpts) (Agent, error) {
	var raw map[string]any
	if err := toml.Unmarshal(normalizeNewlines(src), &raw); err != nil {
		return Agent{}, fmt.Errorf("bad TOML: %w", err)
	}
	fields := map[string]string{}
	for key, value := range raw {
		if s, ok := value.(string); ok {
			fields[key] = s
		} else {
			fields[key] = nonScalar
		}
	}
	if err := checkKeys(sortedKeys(fields), codexKeys, opts.Config.DropKeys); err != nil {
		return Agent{}, err
	}
	name, desc, model, effort, err := commonFields(fields, "model", "model_reasoning_effort")
	if err != nil {
		return Agent{}, err
	}
	body := fields["developer_instructions"]
	if body == nonScalar {
		return Agent{}, errors.New("developer_instructions must be a string")
	}
	model, effort, err = MapModel(opts.Config, name, model, effort, "codex", opts.Flags)
	if err != nil {
		return Agent{}, err
	}
	return Agent{Name: name, Description: desc, Model: model, Effort: effort, Body: body}, nil
}

func commonFields(fields map[string]string, modelKey, effortKey string) (name, desc, model, effort string, err error) {
	for _, key := range []string{"name", "description", modelKey, effortKey} {
		if fields[key] == nonScalar {
			return "", "", "", "", fmt.Errorf("%s must be a string", key)
		}
	}
	name, desc = fields["name"], fields["description"]
	if strings.TrimSpace(name) == "" {
		return "", "", "", "", errors.New("name is missing or empty")
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\:`) || strings.HasPrefix(name, ".") {
		return "", "", "", "", fmt.Errorf("name %q is not a plain file name", name)
	}
	if strings.TrimSpace(desc) == "" {
		return "", "", "", "", errors.New("description is missing or empty")
	}
	return name, desc, fields[modelKey], fields[effortKey], nil
}

func checkKeys(keys, known, drop []string) error {
	for _, key := range keys {
		if slices.Contains(known, key) || slices.Contains(drop, key) {
			continue
		}
		return fmt.Errorf("key %q has no counterpart; add it to sync.agents.drop_keys to discard it", key)
	}
	return nil
}

func codexName(strategy, name string) (string, error) {
	switch strategy {
	case "", "keep":
		return name, nil
	case "underscore":
		return strings.ReplaceAll(name, "-", "_"), nil
	}
	return "", fmt.Errorf("sync.agents.codex_name must be keep or underscore, got %q", strategy)
}

// MapModel maps a model and an effort from the vendor named by from ("claude"
// or "codex") to the other one. The model returned is a target alias or a
// deliberate pin; the caller resolves a Codex alias to a concrete name.
// Precedence from Claude to Codex: flags, overrides for the agent, model_map.
func MapModel(cfg config.AgentSync, name, model, effort, from string, flags map[string]string) (string, string, error) {
	if from != "claude" && from != "codex" {
		return "", "", fmt.Errorf("unknown source agent %q", from)
	}
	forward := map[string]string{}
	for k, v := range cfg.ModelMap {
		forward[k] = v
	}
	if err := checkBijective(forward); err != nil {
		return "", "", err
	}
	if from == "codex" {
		// Flags pick a Claude alias -> Codex alias, so they apply only from
		// Claude to Codex. Merging them here would break the inverse map.
		// A pinned Codex model returns as the latest of its family: the pin
		// is lost on a round trip through Claude (lossy by design).
		return mapToClaude(cfg, forward, model, effort)
	}
	merged := make(map[string]string, len(forward)+len(flags))
	for k, v := range forward {
		merged[k] = v
	}
	for k, v := range flags {
		merged[k] = v
	}
	return mapToCodex(cfg, merged, flags, name, model, effort)
}

func checkBijective(m map[string]string) error {
	seen := map[string]string{}
	for _, k := range sortedKeys(m) {
		if other, dup := seen[m[k]]; dup {
			return fmt.Errorf("model_map is not one to one: %q and %q both map to %q", other, k, m[k])
		}
		seen[m[k]] = k
	}
	return nil
}

func mapToCodex(cfg config.AgentSync, forward, flags map[string]string, name, model, effort string) (string, string, error) {
	alias := ""
	if model != "" {
		alias = models.Normalize("claude", model)
		switch override := cfg.Overrides[name]; {
		case flags[alias] != "":
			alias = flags[alias]
		case override.Model != "":
			alias = override.Model
		case forward[alias] != "":
			alias = forward[alias]
		default:
			return "", "", fmt.Errorf("model %q is not in model_map; known Claude models: %s", model, strings.Join(sortedKeys(forward), ", "))
		}
	}
	if override := cfg.Overrides[name].Effort; override != "" {
		if !knownEffort(cfg.EffortMap, override) {
			return "", "", fmt.Errorf("effort %q (override for %s) is not in effort_map", override, name)
		}
		return alias, override, nil
	}
	if effort == "" {
		if def := cfg.DefaultEffort[models.Normalize("claude", model)]; def != "" && model != "" {
			effort = def
		}
	}
	if effort != "" && !knownEffort(cfg.EffortMap, effort) {
		return "", "", fmt.Errorf("effort %q is not in effort_map", effort)
	}
	return alias, effort, nil
}

func knownEffort(effortMap map[string]string, effort string) bool {
	if _, ok := effortMap[effort]; ok {
		return true
	}
	for _, v := range effortMap {
		if v == effort {
			return true
		}
	}
	return false
}

func mapToClaude(cfg config.AgentSync, forward map[string]string, model, effort string) (string, string, error) {
	if model != "" && !strings.HasPrefix(model, "claude-") {
		alias := models.Normalize("codex", model)
		found := ""
		for _, k := range sortedKeys(forward) {
			if forward[k] == alias {
				found = k
			}
		}
		if found == "" {
			known := make([]string, 0, len(forward))
			for _, v := range forward {
				known = append(known, v)
			}
			sort.Strings(known)
			return "", "", fmt.Errorf("model %q is not in model_map; known Codex models: %s", model, strings.Join(known, ", "))
		}
		model = found
	}
	if effort != "" {
		mapped, ok := cfg.EffortMap[effort]
		if !ok {
			return "", "", fmt.Errorf("effort %q is not in effort_map", effort)
		}
		effort = mapped
	}
	return model, effort, nil
}

// RenderCodexAgent writes a Codex TOML file and reads it back to check it.
func RenderCodexAgent(a Agent) ([]byte, error) {
	if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Description) == "" {
		return nil, errors.New("agent needs a name and a description")
	}
	var b strings.Builder
	b.WriteString(generatedComment + "\n")
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
	got := Agent{Name: back.Name, Description: back.Description, Model: back.Model, Effort: back.Effort, Body: back.Instructions}
	if got != a {
		return nil, fmt.Errorf("rendered TOML reads back differently: got %#v, want %#v", got, a)
	}
	return out, nil
}

// RenderClaudeAgent writes a Claude Markdown file and reads it back to check it.
func RenderClaudeAgent(a Agent) ([]byte, error) {
	if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Description) == "" {
		return nil, errors.New("agent needs a name and a description")
	}
	header, err := renderHeader(a)
	if err != nil {
		return nil, fmt.Errorf("render YAML header: %w", err)
	}
	out := []byte("---\n" + generatedComment + "\n" + string(header) + "---\n" + a.Body)

	h, body, err := splitFrontmatter(out)
	if err != nil {
		return nil, fmt.Errorf("rendered Markdown does not parse: %w", err)
	}
	fields, err := parseHeader(h)
	if err != nil {
		return nil, fmt.Errorf("rendered Markdown does not parse: %w", err)
	}
	got := Agent{Name: fields["name"], Description: fields["description"], Model: fields["model"], Effort: fields["effort"], Body: body}
	if got != a {
		return nil, fmt.Errorf("rendered Markdown reads back differently: got %#v, want %#v", got, a)
	}
	return out, nil
}

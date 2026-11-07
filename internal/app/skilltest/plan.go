// Package skilltest grades skill answers with repeatable text checks.
package skilltest

import (
	"encoding/json"
	"errors"
	"fmt"
	"go.yaml.in/yaml/v3"
	"io"
	"regexp"
	"slices"
	"strings"
)

type Plan struct {
	Situations []Situation `yaml:"situations"`
}
type Situation struct {
	ID     string   `yaml:"id"`
	Agents []string `yaml:"agents,omitempty"`
	Text   string   `yaml:"text"`
	Check  Check    `yaml:"check"`
}
type Check struct {
	Contains []string `yaml:"contains"`
	Absent   []string `yaml:"absent"`
	Starts   string   `yaml:"starts"`
	Regex    string   `yaml:"regex"`
}

// ReadPlan reads a situation plan; agents are the registered agents' names,
// and a situation may name only those.
func ReadPlan(r io.Reader, agents []string) (Plan, error) {
	var p Plan
	d := yaml.NewDecoder(r)
	d.KnownFields(true)
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return p, fmt.Errorf("expected one YAML document")
	}
	if len(p.Situations) == 0 {
		return p, fmt.Errorf("plan needs situations")
	}
	ids := map[string]bool{}
	for _, s := range p.Situations {
		if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Text) == "" || ids[s.ID] {
			return p, fmt.Errorf("each situation needs a unique id and text: %q", s.ID)
		}
		ids[s.ID] = true
		for _, agent := range s.Agents {
			if !slices.Contains(agents, agent) {
				return p, fmt.Errorf("situation %s: unknown agent %q", s.ID, agent)
			}
		}
		c := s.Check
		if len(c.Contains)+len(c.Absent) == 0 && c.Starts == "" && c.Regex == "" {
			return p, fmt.Errorf("situation %s needs a check", s.ID)
		}
		if c.Regex != "" {
			if _, err := regexp.Compile("(?i)" + c.Regex); err != nil {
				return p, fmt.Errorf("situation %s: %w", s.ID, err)
			}
		}
	}
	return p, nil
}

// ForAgent keeps shared situations and those naming the agent, in plan order.
// An omitted or empty agents list applies to both agents.
func (p Plan) ForAgent(agent string) Plan {
	var selected Plan
	for _, s := range p.Situations {
		if len(s.Agents) == 0 || slices.Contains(s.Agents, agent) {
			selected.Situations = append(selected.Situations, s)
		}
	}
	return selected
}

func (c Check) Match(answer string) bool {
	if strings.TrimSpace(answer) == "" {
		return false
	}
	lower := strings.ToLower(answer)
	for _, word := range c.Contains {
		if !strings.Contains(lower, strings.ToLower(word)) {
			return false
		}
	}
	for _, word := range c.Absent {
		if strings.Contains(lower, strings.ToLower(word)) {
			return false
		}
	}
	if c.Starts != "" && !strings.HasPrefix(strings.TrimSpace(lower), strings.ToLower(c.Starts)) {
		return false
	}
	if c.Regex != "" {
		re, err := regexp.Compile("(?i)" + c.Regex)
		if err != nil || !re.MatchString(answer) {
			return false
		}
	}
	return true
}

func (p Plan) Grade(raw string) map[string]bool {
	var answers map[string]string
	if json.Unmarshal([]byte(raw), &answers) != nil {
		answers = nil
	}
	graded := map[string]bool{}
	for _, s := range p.Situations {
		graded[s.ID] = s.Check.Match(answers[s.ID])
	}
	return graded
}

func (p Plan) Prompt(skill string) string {
	var b strings.Builder
	b.WriteString("Follow the skill below. Answer each situation as you would act.\n<skill>\n" + skill + "\n</skill>\nReply with only one JSON object with situation ids as keys and short strings as values. No tools or other text.\n")
	for _, s := range p.Situations {
		fmt.Fprintf(&b, "\n%s: %s\n", s.ID, s.Text)
	}
	return b.String()
}

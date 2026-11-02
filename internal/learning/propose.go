package learning

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"go.yaml.in/yaml/v3"
)

type ClassProposal struct {
	Name     string
	Rule     config.ClassRule
	Class    *config.Class
	Evidence string
	group    string
	features string
	count    int
}

// DeclineKey identifies the feature group independently of its observation
// count, so later delegations cannot move an existing decline to another group.
func (p ClassProposal) DeclineKey() string {
	return fmt.Sprintf("%s@%x", p.Name, sha256.Sum256([]byte(p.group)))
}

// ProposeClasses offers one group per public name: most evidence first, then
// the sorted feature set. Other groups remain available after apply or decline.
func ProposeClasses(cfg *config.Config, ds []journal.Delegation, declined []string) []ClassProposal {
	candidates := classCandidates(cfg, ds)
	slices.SortFunc(candidates, func(a, b ClassProposal) int {
		if n := cmp.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		if n := cmp.Compare(b.count, a.count); n != 0 {
			return n
		}
		if n := cmp.Compare(a.features, b.features); n != 0 {
			return n
		}
		return cmp.Compare(a.group, b.group)
	})
	var proposals []ClassProposal
	seen := map[string]bool{}
	for _, p := range candidates {
		if seen[p.Name] || slices.Contains(declined, p.Name) || slices.Contains(declined, p.DeclineKey()) {
			continue
		}
		seen[p.Name] = true
		proposals = append(proposals, p)
	}
	return proposals
}

func classCandidates(cfg *config.Config, ds []journal.Delegation) []ClassProposal {
	type key struct{ role, stated, rule, features string }
	type group struct {
		features map[string]string
		count    int
	}
	groups := map[key]*group{}
	for _, d := range ds {
		if d.ProfileOf != "" || d.Outcome == journal.OutcomeInfra {
			continue
		}
		features := maps.Clone(d.Features)
		if features == nil {
			features = map[string]string{}
		}
		data, _ := json.Marshal(features)
		add := func(k key) {
			if groups[k] == nil {
				groups[k] = &group{features: features}
			}
			groups[k].count++
		}
		if d.Class == Unclassified {
			add(key{role: d.Role, features: string(data)})
		}
		if d.StatedClass != "" && d.RuleClass != "" && d.StatedClass != d.RuleClass {
			add(key{stated: d.StatedClass, rule: d.RuleClass, features: string(data)})
		}
	}
	var proposals []ClassProposal
	for k, g := range groups {
		add := func(p ClassProposal) {
			key, _ := json.Marshal([]string{k.role, k.stated, k.rule, k.features})
			p.group, p.features, p.count = string(key), k.features, g.count
			proposals = append(proposals, p)
		}
		if k.stated != "" {
			name := "rule:" + k.stated
			if g.count < 3 {
				continue
			}
			if _, ok := cfg.Learning.Classes[k.stated]; !ok {
				continue
			}
			rule := config.ClassRule{When: maps.Clone(g.features), Class: k.stated}
			if slices.ContainsFunc(cfg.Learning.Classify, func(r config.ClassRule) bool { return r.Class == rule.Class && maps.Equal(r.When, rule.When) }) {
				continue
			}
			add(ClassProposal{Name: name, Rule: rule, Evidence: fmt.Sprintf("%d delegations stated %s while the rules chose %s, with features %s", g.count, k.stated, k.rule, k.features)})
			continue
		}
		if g.count < 5 {
			continue
		}
		output := g.features["output"]
		if output == "" {
			output = "misc"
		}
		name := k.role + "_" + output
		role, ok := cfg.Roles[k.role]
		if !ok {
			continue
		}
		when := maps.Clone(g.features)
		when["role"] = k.role
		rule := config.ClassRule{When: when, Class: name}
		if slices.ContainsFunc(cfg.Learning.Classify, func(r config.ClassRule) bool { return r.Class == name && maps.Equal(r.When, when) }) {
			continue
		}
		_, exists := cfg.Learning.Classes[name]
		// After creating this class for one group, subsequent groups add rules
		// for it. A pre-existing class without such a role rule stays untouched.
		if exists && !slices.ContainsFunc(cfg.Learning.Classify, func(r config.ClassRule) bool { return r.Class == name && r.When["role"] == k.role }) {
			continue
		}
		c := &config.Class{Description: fmt.Sprintf("%s work with output %s, proposed from %d unclassified delegations", k.role, output, g.count), Priority: "cost", Roles: []string{k.role}, Rubric: []string{"correct against the tests and the brief", "false claims (count)"}}
		for _, target := range []config.Target{role.Use, role.Fallback} {
			c.Ladder = append(c.Ladder, config.LadderEntry{Agent: target.Agent, Model: target.Model, Effort: target.Effort, Prior: 2.9})
		}
		p := ClassProposal{Name: name, Rule: rule, Evidence: c.Description + ", features " + k.features}
		if !exists {
			p.Class = c
		}
		add(p)
	}
	return proposals
}

// ApplyClass edits only the project's config layer. The full effective rule
// list is retained because a YAML sequence replaces the lower layers' list.
func ApplyClass(root string, cfg *config.Config, p ClassProposal) (old, updated []byte, err error) {
	path := filepath.Join(root, ".duolog", "config.yaml")
	old, err = os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return old, nil, err
	}
	var doc yaml.Node
	if existed && len(bytes.TrimSpace(old)) > 0 {
		if err = yaml.Unmarshal(old, &doc); err != nil {
			return old, nil, err
		}
	} else {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
		if !existed {
			doc.Content[0].HeadComment = "yaml-language-server: $schema=https://raw.githubusercontent.com/lorem-dev/duolog/main/duolog-config.schema.json"
		}
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return old, nil, fmt.Errorf("project config must be a YAML mapping")
	}
	learning, err := classMapping(doc.Content[0], "learning")
	if err != nil {
		return old, nil, err
	}
	var oldRules []*yaml.Node
	if node := classValue(learning, "classify"); node != nil && node.Kind == yaml.SequenceNode {
		oldRules = node.Content
	}
	rules := slices.Clone(cfg.Learning.Classify)
	i := slices.IndexFunc(rules, func(r config.ClassRule) bool {
		return Classify([]config.ClassRule{r}, p.Rule.When["role"], p.Rule.When) != ""
	})
	if i < 0 {
		i = len(rules)
	}
	rules = slices.Insert(rules, i, p.Rule)
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, rule := range rules {
		var encoded yaml.Node
		if err = encoded.Encode(rule); err != nil {
			return old, nil, err
		}
		// Reuse matching source nodes to keep comments on the old rules.
		for _, node := range oldRules {
			var original config.ClassRule
			if node.Decode(&original) == nil && original.Class == rule.Class && maps.Equal(original.When, rule.When) {
				encoded = *node
				break
			}
		}
		sequence.Content = append(sequence.Content, &encoded)
	}
	classSet(learning, "classify", sequence)
	if p.Class != nil {
		classes, e := classMapping(learning, "classes")
		if e != nil {
			return old, nil, e
		}
		var node yaml.Node
		if err = node.Encode(p.Class); err != nil {
			return old, nil, err
		}
		classSet(classes, p.Name, &node)
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err = encoder.Encode(&doc); err != nil {
		return old, nil, err
	}
	if err = encoder.Close(); err != nil {
		return old, nil, err
	}
	updated = buf.Bytes()
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return old, nil, err
	}
	if err = os.WriteFile(path, updated, 0600); err != nil {
		return old, nil, err
	}
	loaded, loadErr := config.Load(root)
	err = loadErr
	if err == nil && Classify(loaded.Learning.Classify, p.Rule.When["role"], p.Rule.When) != p.Rule.Class {
		err = fmt.Errorf("proposal %q is overridden by a later config layer", p.Name)
	}
	if err != nil {
		var restore error
		if existed {
			restore = os.WriteFile(path, old, 0600)
		} else {
			restore = os.Remove(path)
		}
		return old, updated, errors.Join(err, restore)
	}
	return old, updated, nil
}

func classValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func classSet(node *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			previous := node.Content[i+1]
			value.HeadComment = previous.HeadComment
			value.LineComment = previous.LineComment
			value.FootComment = previous.FootComment
			node.Content[i+1] = value
			return
		}
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func classMapping(parent *yaml.Node, key string) (*yaml.Node, error) {
	if node := classValue(parent, key); node != nil {
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s must be a YAML mapping", key)
		}
		return node, nil
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	classSet(parent, key, node)
	return node, nil
}

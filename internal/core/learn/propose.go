package learn

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/record"
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
func ProposeClasses(cfg *config.Config, ds []record.Delegation, declined []string) []ClassProposal {
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

func classCandidates(cfg *config.Config, ds []record.Delegation) []ClassProposal {
	type key struct{ role, stated, rule, features string }
	type group struct {
		features map[string]string
		count    int
	}
	groups := map[key]*group{}
	for _, d := range ds {
		if d.ProfileOf != "" || d.Outcome == record.OutcomeInfra {
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

// VerifyClass reports a proposal whose rule a later config layer overrides.
func VerifyClass(cfg *config.Config, p ClassProposal) error {
	if Classify(cfg.Learning.Classify, p.Rule.When["role"], p.Rule.When) != p.Rule.Class {
		return fmt.Errorf("proposal %q is overridden by a later config layer", p.Name)
	}
	return nil
}

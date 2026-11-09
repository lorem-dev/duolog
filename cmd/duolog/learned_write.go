package main

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"go.yaml.in/yaml/v3"
)

const learnedHeader = "# Commands a person allowed for workers with `duolog remember`. Deny rules still win.\n"

type learnedFile struct {
	Rules []config.Rule `yaml:"rules"`
}

func sameRule(a, b config.Rule) bool {
	return a.Action == b.Action && a.Match == b.Match && a.Decision == b.Decision && a.ReadOnly == b.ReadOnly && slices.Equal(a.Roles, b.Roles)
}

// locateRules finds where new items go: the text to write before them (the
// rules key when the file has none) and the column of the dash of the items.
func locateRules(top *yaml.Node, nl string) (string, int, error) {
	if top.Kind != yaml.MappingNode || top.Style&yaml.FlowStyle != 0 {
		return "", 0, fmt.Errorf("the file is not a block mapping, so a rule cannot be appended without rewriting it")
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "rules" {
			continue
		}
		seq := top.Content[i+1]
		if i+2 != len(top.Content) {
			return "", 0, fmt.Errorf("rules is not the last key, so a rule cannot be appended without rewriting the file")
		}
		if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 || len(seq.Content) == 0 {
			return "", 0, fmt.Errorf("rules is not a block list with items, so a rule cannot be appended without rewriting the file")
		}
		return "", seq.Column - 1, nil
	}
	return "rules:" + nl, 0, nil
}

// appendLearnedRule returns data with rule added at the end and every existing
// byte untouched. It refuses, with the reason, when it cannot add the rule that
// way or when the result would not read back as the old rules plus the new one.
func appendLearnedRule(data []byte, old []config.Rule, rule config.Rule) ([]byte, error) {
	nl := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		nl = "\r\n"
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	prefix, indent := "rules:"+nl, 0
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		var err error
		if prefix, indent, err = locateRules(doc.Content[0], nl); err != nil {
			return nil, err
		}
	}
	item, err := yaml.Marshal([]config.Rule{rule})
	if err != nil {
		return nil, err
	}
	var add strings.Builder
	add.WriteString(prefix)
	for _, line := range strings.Split(strings.TrimSuffix(string(item), "\n"), "\n") {
		add.WriteString(strings.Repeat(" ", indent) + line + nl)
	}
	out := slices.Clone(data)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, nl...)
	}
	out = append(out, add.String()...)
	var back learnedFile
	if err := yaml.Unmarshal(out, &back); err != nil {
		return nil, fmt.Errorf("the appended file does not parse: %w", err)
	}
	want := append(slices.Clone(old), rule)
	if !slices.EqualFunc(back.Rules, want, sameRule) {
		return nil, fmt.Errorf("the appended file would not read back as the old rules plus the new one")
	}
	return out, nil
}

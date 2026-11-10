package config

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestAliasExpansionIsBounded(t *testing.T) {
	// Mutation killed: recursive alias copying has no total expansion budget.
	leaf := &yaml.Node{Kind: yaml.ScalarNode, Value: "x"}
	for range 6 {
		parent := &yaml.Node{Kind: yaml.SequenceNode}
		for range 10 {
			parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.AliasNode, Alias: leaf})
		}
		leaf = parent
	}
	_, err := resolveYAMLNode(leaf, map[*yaml.Node]bool{})
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("%v", err)
	}
}

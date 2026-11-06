// Package learn writes learning-mode proposals into the project's config.
package learn

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/lorem-dev/duolog/internal/config"
	corelearn "github.com/lorem-dev/duolog/internal/core/learn"
	"go.yaml.in/yaml/v3"
)

// ApplyClass edits only the project's config layer. The full effective rule
// list is retained because a YAML sequence replaces the lower layers' list.
func ApplyClass(root string, cfg *config.Config, p corelearn.ClassProposal) (old, updated []byte, err error) {
	return EditConfig(root, func(learning *yaml.Node) error {
		_, err := AddClass(learning, cfg.Learning.Classify, p)
		return err
	}, func(loaded *config.Config) error {
		return corelearn.VerifyClass(loaded, p)
	})
}

// AddClass writes p into the learning mapping of the project's config: its
// rule goes into rules, the full effective list, before the first rule that
// already matches its features, and a new class goes under classes. It
// returns the extended list for the next proposal of the same edit.
func AddClass(learning *yaml.Node, rules []config.ClassRule, p corelearn.ClassProposal) ([]config.ClassRule, error) {
	var oldRules []*yaml.Node
	if node := classValue(learning, "classify"); node != nil && node.Kind == yaml.SequenceNode {
		oldRules = node.Content
	}
	rules = slices.Clone(rules)
	i := slices.IndexFunc(rules, func(r config.ClassRule) bool {
		return corelearn.Classify([]config.ClassRule{r}, p.Rule.When["role"], p.Rule.When) != ""
	})
	if i < 0 {
		i = len(rules)
	}
	rules = slices.Insert(rules, i, p.Rule)
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, rule := range rules {
		var encoded yaml.Node
		if err := encoded.Encode(rule); err != nil {
			return nil, err
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
		classes, err := classMapping(learning, "classes")
		if err != nil {
			return nil, err
		}
		var node yaml.Node
		if err := node.Encode(p.Class); err != nil {
			return nil, err
		}
		classSet(classes, p.Name, &node)
	}
	return rules, nil
}

// SetClassChecks sets the checks of class in the learning mapping of the
// project's config and leaves the class's other fields to the lower layers.
func SetClassChecks(learning *yaml.Node, class string, checks []string) error {
	classes, err := classMapping(learning, "classes")
	if err != nil {
		return err
	}
	node, err := classMapping(classes, class)
	if err != nil {
		return err
	}
	var encoded yaml.Node
	if err := encoded.Encode(checks); err != nil {
		return err
	}
	classSet(node, "checks", &encoded)
	return nil
}

// EditConfig applies edit to the learning mapping of the project's config
// layer, keeping comments, and writes the result. It then loads the merged
// config and runs verify, when given, on it; a load or verify error restores
// the file as it was.
func EditConfig(root string, edit func(learning *yaml.Node) error, verify func(*config.Config) error) (old, updated []byte, err error) {
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
	if err = edit(learning); err != nil {
		return old, nil, err
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
	if err == nil && verify != nil {
		err = verify(loaded)
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

package agentsync

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// convertSkill rewrites the frontmatter of a SKILL.md and leaves the body
// byte for byte as it was. Keys in drop are removed; every other key stays,
// because both agents ignore keys they do not know. fromClaude adds a warning
// for disable-model-invocation, which Codex does not honour.
func convertSkill(src []byte, drop []string, fromClaude bool) (out []byte, warnings []string, err error) {
	header, body, err := splitFrontmatter(src)
	if err != nil {
		return nil, nil, err
	}
	if _, err := parseHeader(header); err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	// A generated skill can be the source after a switch of direction.
	if err := yaml.Unmarshal([]byte(strings.TrimPrefix(header, generatedComment+"\n")), &doc); err != nil {
		return nil, nil, fmt.Errorf("bad YAML header: %w", err)
	}
	root := doc.Content[0]
	kept := make([]*yaml.Node, 0, len(root.Content))
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		if slices.Contains(drop, key) {
			continue
		}
		if fromClaude && key == "disable-model-invocation" {
			warnings = append(warnings, "disable-model-invocation is not honoured by Codex; the model may still pick this skill")
		}
		kept = append(kept, root.Content[i], root.Content[i+1])
	}
	root.Content = kept
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, nil, fmt.Errorf("render YAML header: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, nil, err
	}
	rendered := buf.String()
	if len(kept) == 0 {
		rendered = ""
	}
	out = []byte("---\n" + generatedComment + "\n" + rendered + "---\n" + body)
	if h, b, err := splitFrontmatter(out); err != nil || b != body {
		return nil, nil, fmt.Errorf("rendered SKILL.md does not parse back: %v", err)
	} else if _, err := parseHeader(h); err != nil {
		return nil, nil, fmt.Errorf("rendered SKILL.md does not parse back: %w", err)
	}
	return out, warnings, nil
}

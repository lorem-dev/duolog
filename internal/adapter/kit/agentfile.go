package kit

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lorem-dev/duolog/internal/core/agentfile"
)

// NormalizeNewlines turns CRLF into LF so every later step sees one form.
func NormalizeNewlines(src []byte) []byte {
	return bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
}

// SplitFrontmatter returns the YAML header and the body of a Markdown file.
// The header ends at the first line that is exactly "---".
func SplitFrontmatter(src []byte) (header, body string, err error) {
	text := string(NormalizeNewlines(bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))))
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return "", "", errors.New("no YAML header: the file must start with a --- line")
	}
	pos := 0
	for pos <= len(rest) {
		end := strings.IndexByte(rest[pos:], '\n')
		var line string
		if end < 0 {
			line = rest[pos:]
		} else {
			line = rest[pos : pos+end]
		}
		if line == "---" {
			if end < 0 {
				return rest[:pos], "", nil
			}
			return rest[:pos], rest[pos+end+1:], nil
		}
		if end < 0 {
			break
		}
		pos += end + 1
	}
	return "", "", errors.New("YAML header is not closed by a --- line")
}

// ParseHeader decodes a header into scalar fields. Any non-scalar value is
// reported by key; duplicates are refused.
func ParseHeader(header string) (map[string]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(header), &doc); err != nil {
		return nil, fmt.Errorf("bad YAML header: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("YAML header is not a mapping")
	}
	root := doc.Content[0]
	fields := map[string]string{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		if key.Kind != yaml.ScalarNode {
			return nil, errors.New("YAML header has a key that is not a string")
		}
		if _, dup := fields[key.Value]; dup {
			return nil, fmt.Errorf("duplicate key %q in YAML header", key.Value)
		}
		switch {
		case val.Kind == yaml.ScalarNode && val.Tag == "!!null":
			fields[key.Value] = ""
		case val.Kind == yaml.ScalarNode:
			fields[key.Value] = val.Value
		default:
			// Keep the key so that unknown-key checks still see it; known
			// string keys reject this marker.
			fields[key.Value] = agentfile.NonScalar
		}
	}
	return fields, nil
}

// RenderHeader writes the YAML header of a Markdown agent file.
func RenderHeader(a agentfile.Agent) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key, value string) {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
	add("name", a.Name)
	add("description", a.Description)
	if a.Model != "" {
		add("model", a.Model)
	}
	if a.Effort != "" {
		add("effort", a.Effort)
	}
	return yaml.Marshal(root)
}

// ConvertSkill rewrites the frontmatter of a SKILL.md and leaves the body
// byte for byte as it was. Keys in drop are removed; every other key stays,
// because both agents ignore keys they do not know. A kept key named warnKey
// adds warning, which tells that the other agent does not honour it.
func ConvertSkill(src []byte, drop []string, warnKey, warning string) (out []byte, warnings []string, err error) {
	header, body, err := SplitFrontmatter(src)
	if err != nil {
		return nil, nil, err
	}
	if _, err := ParseHeader(header); err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	// A generated skill can be the source after a switch of direction.
	if err := yaml.Unmarshal([]byte(strings.TrimPrefix(header, agentfile.GeneratedComment+"\n")), &doc); err != nil {
		return nil, nil, fmt.Errorf("bad YAML header: %w", err)
	}
	root := doc.Content[0]
	kept := make([]*yaml.Node, 0, len(root.Content))
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		if slices.Contains(drop, key) {
			continue
		}
		if warnKey != "" && key == warnKey {
			warnings = append(warnings, warning)
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
	out = []byte("---\n" + agentfile.GeneratedComment + "\n" + rendered + "---\n" + body)
	if h, b, err := SplitFrontmatter(out); err != nil || b != body {
		return nil, nil, fmt.Errorf("rendered SKILL.md does not parse back: %v", err) //nolint:errorlint // err is nil when only the body differs, and %w would print %!w(<nil>)
	} else if _, err := ParseHeader(h); err != nil {
		return nil, nil, fmt.Errorf("rendered SKILL.md does not parse back: %w", err)
	}
	return out, warnings, nil
}

// Package task validates handoffs and builds bounded worker input.
package task

import (
	"bytes"
	"fmt"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var sections = []string{"Goal", "Read", "Write", "Limits", "Check"}

func parse(data []byte) (map[string]string, error) {
	result := make(map[string]string)
	current := ""
	next := 0
	fence := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
		}
		if fence == "" && strings.HasPrefix(line, "## ") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			if next >= len(sections) || name != sections[next] {
				return nil, fmt.Errorf("task sections must be Goal, Read, Write, Limits, Check in order; unexpected %q", name)
			}
			current = name
			next++
			continue
		}
		if current != "" {
			result[current] += line + "\n"
		}
	}
	if next != len(sections) {
		return nil, fmt.Errorf("task missing section %s", sections[next])
	}
	return result, nil
}

func Validate(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = parse(data)
	return err
}

type Reference struct {
	Path        string
	First, Last int
}

var rangeSuffix = regexp.MustCompile(`^(.*):([0-9]+)(?:-([0-9]+))?$`)

// References returns the file paths in Read, preserving inclusive line ranges.
func References(path string) ([]Reference, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parts, err := parse(data)
	if err != nil {
		return nil, err
	}
	var refs []Reference
	for _, line := range strings.Split(parts["Read"], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		line = strings.TrimSpace(line[2:])
		var name string
		if strings.HasPrefix(line, "`") {
			end := strings.Index(line[1:], "`")
			if end < 0 {
				return nil, fmt.Errorf("unclosed Read path")
			}
			name = line[1 : end+1]
			tail := line[end+2:]
			if strings.HasPrefix(tail, ":") && len(tail) > 1 && tail[1] >= '0' && tail[1] <= '9' {
				name += strings.Fields(tail)[0]
			}
		} else if fields := strings.Fields(line); len(fields) > 0 {
			name = fields[0]
		}
		name = strings.TrimSuffix(name, ":")
		if name == "" {
			continue
		}
		r := Reference{Path: name}
		if match := rangeSuffix.FindStringSubmatch(name); match != nil {
			r.Path = match[1]
			r.First, _ = strconv.Atoi(match[2])
			r.Last = r.First
			if match[3] != "" {
				r.Last, _ = strconv.Atoi(match[3])
			}
			if r.First < 1 || r.Last < r.First {
				return nil, fmt.Errorf("invalid Read range %q", name)
			}
		}
		r.Path = pathutil.Clean(r.Path)
		refs = append(refs, r)
	}
	return refs, nil
}

// Resolve refuses traversal and symlinks before opening any task input.
func Resolve(root, path string) (string, error) {
	path = pathutil.Native(path)
	if pathutil.IsAbs(path) && !filepath.IsAbs(path) {
		return "", fmt.Errorf("path outside root: %s", path)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path outside root: %s", path)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink input refused: %s", current)
		}
	}
	return path, nil
}

func (r Reference) Text(root string) ([]byte, error) {
	path, err := Resolve(root, r.Path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if r.First == 0 {
		return data, nil
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if r.Last > len(lines) {
		return nil, fmt.Errorf("Read range exceeds %s", r.Path)
	}
	return bytes.Join(lines[r.First-1:r.Last], nil), nil
}

func Inline(path, root string, maxBytes int, allowRead ...func(string) bool) ([]byte, error) {
	// The task copy can live in the shared state of another worktree. Its source
	// is resolved by the caller; read references are still confined to work root.
	var err error
	if _, resolveErr := Resolve(root, path); resolveErr != nil {
		shared := filepath.Join(project.StateRoot(root), ".duolog", "tasks")
		// The copy path can use an OS alias of the shared state directory.
		candidate := path
		if canonical, e := filepath.EvalSymlinks(path); e == nil {
			candidate = canonical
		}
		if canonical, e := filepath.EvalSymlinks(shared); e == nil {
			shared = canonical
		}
		if _, err := Resolve(shared, candidate); err != nil {
			return nil, resolveErr
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	refs, err := References(path)
	if err != nil {
		return nil, err
	}
	var refused []string
	for _, ref := range refs {
		if len(allowRead) > 0 && !allowRead[0](ref.Path) {
			refused = append(refused, ref.Path)
		}
	}
	if len(refused) > 0 {
		data = []byte(Annotate(string(data), "Not inlined by read policy: "+strings.Join(refused, ", ")+". Keep these as references.\n"))
	}
	block := append([]byte("<stdin>\n"), data...)
	suffix := []byte("\n</stdin>\n")
	if len(block)+len(suffix) > maxBytes {
		return nil, fmt.Errorf("task exceeds inline_max_bytes (%d)", maxBytes)
	}
	for _, ref := range refs {
		if len(allowRead) > 0 && !allowRead[0](ref.Path) {
			continue
		}
		if _, err := Resolve(root, ref.Path); err != nil {
			return nil, err
		}
		content, err := ref.Text(root)
		if err != nil {
			continue
		}
		label := ref.Path
		if ref.First > 0 {
			label += fmt.Sprintf(":%d-%d", ref.First, ref.Last)
		}
		addition := append([]byte("\n<read path="+strconv.Quote(label)+">\n"), content...)
		addition = append(addition, []byte("\n</read>\n")...)
		if len(block)+len(addition)+len(suffix) <= maxBytes {
			block = append(block, addition...)
		}
	}
	return append(block, suffix...), nil
}

// Annotate uses the same fence and heading rules as the task parser.
func Annotate(text, lines string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	fence, offset := "", 0
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
		}
		offset += len(line)
		if fence == "" && trimmed == "## Limits" {
			return text[:offset] + lines + text[offset:]
		}
	}
	return text
}

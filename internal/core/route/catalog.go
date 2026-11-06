// Package route holds the model catalog rules: which concrete model a family
// means, and which family a concrete model belongs to.
package route

import (
	"strconv"
	"strings"
)

// Entry contains the catalog fields needed for version selection.
type Entry struct {
	Slug       string `json:"slug"`
	Visibility string `json:"visibility"`
}

// Latest returns the highest numeric version of a visible GPT family.
func Latest(family string, list []Entry) (string, bool) {
	var best []int
	var name string
	for _, entry := range list {
		if entry.Visibility == "hide" {
			continue
		}
		version, modelFamily, ok := splitGPT(entry.Slug)
		if !ok || modelFamily != family {
			continue
		}
		if name == "" || newer(version, best) {
			name, best = entry.Slug, version
		}
	}
	return name, name != ""
}

func splitGPT(name string) ([]int, string, bool) {
	rest, ok := strings.CutPrefix(name, "gpt-")
	if !ok {
		return nil, "", false
	}
	version, family, ok := strings.Cut(rest, "-")
	if !ok || family == "" {
		return nil, "", false
	}
	var numbers []int
	for _, part := range strings.Split(version, ".") {
		if part == "" {
			return nil, "", false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return nil, "", false
			}
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, "", false
		}
		numbers = append(numbers, n)
	}
	return numbers, family, true
}

func newer(a, b []int) bool {
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

// Normalize maps a concrete vendor id to its family alias.
func Normalize(agent, concrete string) string {
	if agent == "codex" {
		if _, family, ok := splitGPT(concrete); ok {
			return family
		}
	}
	if agent == "claude" {
		if rest, ok := strings.CutPrefix(concrete, "claude-"); ok {
			parts := strings.Split(rest, "-")
			for i := 1; i < len(parts); i++ {
				if _, err := strconv.Atoi(parts[i]); err == nil {
					return strings.Join(parts[:i], "-")
				}
			}
		}
	}
	return concrete
}

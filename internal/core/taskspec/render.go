package taskspec

import (
	"errors"
	"fmt"
	"strings"
)

// Spec is a task file as data. Render writes the five sections in order.
type Spec struct {
	Goal                       string
	Read, Write, Limits, Check []string
}

// Render returns the task file text for s. Goal is a paragraph; each Read and
// Write item is a bullet with the path in backticks (a path:from-to range
// stays inside them); Limits items are plain bullets; a Check item is a
// command in backticks, so the preflight reads it (a plain bullet when it has
// a backtick of its own, which the preflight then names as skipped); an empty
// list renders "- none". It refuses an empty goal, an item with a newline,
// and any line that starts with "#" or a code fence, so no input can add or
// move a section. The result passes Validate.
func Render(s Spec) ([]byte, error) {
	goal := strings.ReplaceAll(strings.TrimSpace(s.Goal), "\r\n", "\n")
	if goal == "" {
		return nil, errors.New("task goal is empty")
	}
	for line := range strings.SplitSeq(goal, "\n") {
		if err := checkLine("goal", line); err != nil {
			return nil, err
		}
	}
	var b strings.Builder
	b.WriteString("## Goal\n\n" + goal + "\n")
	for _, list := range []struct {
		name   string
		items  []string
		quoted bool
	}{
		{"Read", s.Read, true},
		{"Write", s.Write, true},
		{"Limits", s.Limits, false},
		{"Check", s.Check, false},
	} {
		b.WriteString("\n## " + list.name + "\n\n")
		// A lone "none" is how a caller says the list is empty.
		if len(list.items) == 0 || (list.name == "Check" && len(list.items) == 1 && strings.EqualFold(strings.TrimSpace(list.items[0]), "none")) {
			b.WriteString("- none\n")
			continue
		}
		for _, item := range list.items {
			item = strings.TrimSpace(item)
			if err := checkItem(list.name, item, list.quoted); err != nil {
				return nil, err
			}
			if list.quoted || (list.name == "Check" && !strings.Contains(item, "`")) {
				item = "`" + item + "`"
			}
			b.WriteString("- " + item + "\n")
		}
	}
	return []byte(b.String()), nil
}

// checkItem refuses what would not stay one bullet of its section.
func checkItem(section, item string, quoted bool) error {
	switch {
	case item == "":
		return fmt.Errorf("task %s has an empty item", section)
	case strings.ContainsAny(item, "\r\n"):
		return fmt.Errorf("task %s item has a line break; give one item per entry", section)
	case quoted && strings.Contains(item, "`"):
		return fmt.Errorf("task %s item has a backtick; give a plain path", section)
	}
	return checkLine(section, item)
}

// checkLine refuses a line that the task parser would read as a heading or a
// fence, whatever its indent.
func checkLine(field, line string) error {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "#"):
		return fmt.Errorf("task %s has a line that starts with #; a heading would change the sections", field)
	case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
		return fmt.Errorf("task %s has a code fence; leave fences out of the task fields", field)
	}
	return nil
}

package screen

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// RunHereError carries the line that tells the lead where to do the task now.
type RunHereError struct{ Line string }

func (e *RunHereError) Error() string { return e.Line }

// OperationOutcome names how an open, send or close ended.
func OperationOutcome(state State, err error) string {
	var here *RunHereError
	switch {
	case errors.As(err, &here):
		return "lead"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "interrupted"
	case err != nil:
		return "failed"
	}
	switch state {
	case Ready:
		return "done"
	case Question, Interrupted:
		return "needs"
	case Limited:
		return "lead"
	}
	// Closed, exited, or still starting when the start time ran out.
	return "failed"
}

// StuckInput reports an idle input line that still holds the sent task line.
func StuckInput(s Screen, root, taskPath string) bool {
	return s.Composer && !s.Working && s.Input != "" && strings.HasPrefix(Squash(strings.Join(s.Rows, "")), Squash(probe(TaskLine(root, taskPath))))
}

// TaskLine is the line typed into the pane. It names the task file from the
// pane's folder and stays short, so a usual task path keeps it on one row of
// an 80-column pane.
func TaskLine(root, path string) string {
	ref := path
	if rel, err := filepath.Rel(root, path); err == nil && filepath.IsLocal(rel) {
		ref = rel
	}
	return "Read " + ref + " and do it. Do not call rex. Reply \"done\"."
}

// probe is the start of a line: enough to tell it from a line that lost its start.
func probe(text string) string { return Cut(text, 40) }

// Squash drops all spaces: a row may break inside a word or at a space.
func Squash(s string) string { return strings.Join(strings.Fields(s), "") }

// Pick finds an option by number, exact label, unique prefix or unique part.
func Pick(options []Option, choice string) (int, error) {
	if n, err := strconv.Atoi(choice); err == nil {
		for i, o := range options {
			if o.Number == n {
				return i, nil
			}
		}
	}
	want := strings.ToLower(choice)
	for _, match := range []func(label string) bool{
		func(l string) bool { return l == want },
		func(l string) bool { return strings.HasPrefix(l, want) },
		func(l string) bool { return strings.Contains(l, want) },
	} {
		found := -1
		for i, o := range options {
			if match(strings.ToLower(o.Label)) {
				if found >= 0 {
					found = -2
					break
				}
				found = i
			}
		}
		if found >= 0 {
			return found, nil
		}
		if found == -2 {
			return 0, fmt.Errorf("%q matches more than one option", choice)
		}
	}
	var labels []string
	for _, o := range options {
		labels = append(labels, o.Label)
	}
	return 0, fmt.Errorf("no option %q; the options are: %s", choice, strings.Join(labels, " | "))
}

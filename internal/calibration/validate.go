package calibration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/journal"
)

// validationRuns is how often the checks run on each side with hidden
// tests, so a flaky test shows as mixed results.
const validationRuns = 3

// judgeOnlyClasses may be judged with the reference as evidence when no
// test or check tells the parent from the reference.
var judgeOnlyClasses = []string{"docs", "mechanical"}

// Validate finds the best verification of c, or the reason it has none:
// "hidden" when its tests, hidden from a candidate, fail on the parent and
// pass on the reference; "check" when a class check does; "judge_only" for
// the docs and mechanical classes. Each side runs in its own export.
// A check run that takes longer than limit excludes c with "check time cap";
// an interrupt returns ctx's error.
func Validate(ctx context.Context, root string, c Candidate, checks []string, limit time.Duration, run CheckRunner) (verification, reason string, err error) {
	if len(checks) > 0 {
		verification, reason, err = validateChecks(ctx, root, c, checks, limit, run)
		if verification != "" || reason != "" || err != nil {
			return verification, reason, err
		}
	}
	if slices.Contains(judgeOnlyClasses, c.Class) {
		return "judge_only", "", nil
	}
	return "", "no verification", nil
}

// validateChecks returns a verification or a reason from the checks, or
// neither when they cannot tell the parent from the reference.
func validateChecks(ctx context.Context, root string, c Candidate, checks []string, limit time.Duration, run CheckRunner) (verification, reason string, err error) {
	parent, cleanParent, err := NewTree(ctx, root, c.Parent)
	if err != nil {
		return "", "", err
	}
	defer func() { err = errors.Join(err, cleanParent()) }()
	reference, cleanReference, err := NewTree(ctx, root, c.Ref)
	if err != nil {
		return "", "", err
	}
	defer func() { err = errors.Join(err, cleanReference()) }()

	// check runs the checks once in dir; a run past limit ends validation
	// with "check time cap", an interrupt with ctx's error.
	timedOut := false
	check := func(dir string) (bool, error) {
		result, over := runChecks(ctx, run, limit, dir, checks)
		timedOut = timedOut || over
		return passed(result), ctx.Err()
	}

	if len(c.Hidden) == 0 {
		parentPassed, err := check(parent)
		if err != nil {
			return "", "", err
		}
		referencePassed, err := check(reference)
		switch {
		case err != nil:
			return "", "", err
		case timedOut:
			return "", "check time cap", nil
		case !referencePassed:
			return "", "checks fail on the reference", nil
		case !parentPassed:
			return "check", "", nil
		}
		return "", "", nil
	}

	for _, name := range c.Hidden {
		if err := copyFile(filepath.Join(reference, filepath.FromSlash(name)), filepath.Join(parent, filepath.FromSlash(name))); err != nil {
			return "", "", err
		}
	}
	var parentPasses, referencePasses int
	for _, side := range []struct {
		dir    string
		passes *int
	}{{parent, &parentPasses}, {reference, &referencePasses}} {
		for range validationRuns {
			ok, err := check(side.dir)
			if err != nil {
				return "", "", err
			}
			if ok {
				*side.passes++
			}
		}
	}
	mixed := func(n int) bool { return n > 0 && n < validationRuns }
	switch {
	case timedOut:
		return "", "check time cap", nil
	case mixed(parentPasses) || mixed(referencePasses):
		return "", "flaky hidden tests", nil
	case referencePasses < validationRuns:
		return "", "checks fail on the reference", nil
	case parentPasses == 0:
		return "hidden", "", nil
	}
	// The hidden tests pass on the parent too: they do not test the change.
	return "", "", nil
}

// runChecks runs commands in dir under a context that ends at limit.
// timedOut says the limit, not ctx, ended a check that did not pass.
func runChecks(ctx context.Context, run CheckRunner, limit time.Duration, dir string, commands []string) (checks []journal.Check, timedOut bool) {
	checkCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	checks = run(checkCtx, dir, commands)
	return checks, ctx.Err() == nil && errors.Is(checkCtx.Err(), context.DeadlineExceeded) && !passed(checks)
}

func passed(checks []journal.Check) bool {
	return !slices.ContainsFunc(checks, func(c journal.Check) bool { return c.Exit != 0 })
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

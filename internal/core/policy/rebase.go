package policy

import (
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// ExpandBraces returns the alternatives of a rule's pattern, one for each
// choice of every {a,b} group, as the matcher reads them.
func ExpandBraces(pattern string) []string {
	return expandBraces(pattern)
}

// anchorAction is a with its read or write target as rule r compares it
// (anchoredTarget), and whether that comparison folds case (foldCase).
func anchorAction(r config.Rule, a Action) (Action, bool) {
	if a.Kind == "read" || a.Kind == "write" {
		a.Target = anchoredTarget(r, a.Target)
	}
	return a, foldCase(r, a)
}

// anchoredTarget is the path a rule compares with target, a path relative to
// the work root. A rule without Under compares target itself. A relative
// Under names the work project inside the lead's project, so a target inside
// the work root is put below it and the lead's rule is read where the lead
// wrote it; a target outside the work root stays as it is, as it does for
// every rule. An absolute Under is the work root, so an absolute selector
// compares the absolute target.
func anchoredTarget(r config.Rule, target string) string {
	switch {
	case r.Under == "" || pathutil.IsAbs(target) || strings.HasPrefix(target, "~"):
		return target
	case pathutil.IsAbs(r.Under):
		return pathutil.Clean(pathutil.Slashes(r.Under) + "/" + target)
	case pathutil.Outside(target):
		return target
	}
	return pathutil.Clean(pathutil.Slashes(r.Under) + "/" + target)
}

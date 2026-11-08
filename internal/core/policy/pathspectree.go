package policy

import (
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// pathspecDenied reports whether a Git pathspec glob is to be denied. A glob
// that spells a denied name, with a slash in place of a wildcard if need be,
// is denied whatever the tree holds, since the name may be in the history. A
// glob that spells only a dot and a letter of an extension, as main.p* does,
// is denied too, unless the tree shows what it means: files that it matches,
// none of them denied. A shorter glob, as .e* and se* are, says too little
// alone and is denied only when it matches a denied file in the tree whose
// name it spells (spellsName). Wide globs such as * and *.go are left to ask.
func pathspecDenied(p config.Policy, lead LeadRules, a Action, target string, denies []string) bool {
	variants := slashVariants(strings.ToLower(pathutil.Slashes(target)))
	aimed := func(strict bool) bool {
		return slices.ContainsFunc(variants, func(variant string) bool { return globAimedAtDeny(variant, denies, strict) })
	}
	if aimed(true) || slices.ContainsFunc(variants, statePattern) {
		return true
	}
	tree := pathspecInTree(p, lead, a, target, denies)
	if aimed(false) {
		return !tree.known || !tree.matched || tree.denied
	}
	return tree.denied
}

// treeMatches is what a walk of the working tree found for a pathspec glob.
// known is false when the walk could not finish.
type treeMatches struct {
	known, matched, denied bool
}

// pathspecInTree walks the tree below the fixed start of a pathspec glob, as
// Git reads it from the working folder, within the budget of one command line.
func pathspecInTree(p config.Policy, lead LeadRules, a Action, target string, denies []string) treeMatches {
	folder := a.Cwd
	if folder == "" {
		folder = a.Work
	}
	glob := strings.ToLower(pathutil.Slashes(target))
	if folder == "" || a.Work == "" || pathutil.IsAbs(glob) || strings.HasPrefix(glob, "../") {
		return treeMatches{}
	}
	tokens := globTokens(glob)
	start := filepath.Join(folder, pathutil.Native(path.Dir(glob[:strings.IndexAny(glob, "*?[")+1])))
	budget := newGlobBudget()
	var tree treeMatches
	err := filepath.WalkDir(start, func(name string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return fs.SkipDir
		case !budget.spend(1):
			return fs.ErrInvalid
		case entry.IsDir() && entry.Name() == ".git":
			return fs.SkipDir
		case entry.IsDir():
			return nil
		}
		rel, err := filepath.Rel(folder, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !pathspecMatches(tokens, strings.ToLower(rel)) {
			return nil
		}
		tree.matched = true
		read := Action{Kind: "read", Target: RelativeTarget(a.Work, a.Cwd, name), Role: a.Role, Shell: a.Shell}
		if Evaluate(p, lead, mode.Full, read).Kind == Deny && spellsName(tokens, rel, denies) {
			tree.denied = true
			return fs.SkipAll
		}
		return nil
	})
	tree.known = err == nil
	return tree
}

// pathspecMatches reports whether a glob, as tokens of globTokens, matches
// name as Git reads a pathspec: * and ? match a slash too.
func pathspecMatches(tokens []string, name string) bool {
	runes := []rune(name)
	reached := make([]bool, len(runes)+1)
	reached[0] = true
	for _, token := range tokens {
		next := make([]bool, len(runes)+1)
		for j := range reached {
			switch {
			case !reached[j]:
			case token == "*":
				for k := j; k <= len(runes); k++ {
					next[k] = true
				}
			case j < len(runes) && tokenMeets(token, string(runes[j])):
				next[j+1] = true
			}
		}
		reached = next
	}
	return reached[len(runes)]
}

// spellsName reports whether a glob, as tokens, holds at least two literal
// characters and they stand, in order, in a segment of rel that a deny names:
// se* spells secrets and a*m spells a.pem, but *.go says nothing of secrets in
// secrets/x.go, and src/* nothing of .env in src/.env.
func spellsName(tokens []string, rel string, denies []string) bool {
	literals := slices.DeleteFunc(slices.Clone(tokens), func(token string) bool { return !literalToken(token) || token == "/" })
	if len(literals) < 2 {
		return false
	}
	return slices.ContainsFunc(strings.Split(strings.ToLower(rel), "/"), func(segment string) bool {
		return namedByDeny(segment, denies) && inOrder(literals, segment)
	})
}

// namedByDeny reports whether a path segment is the state folder or a name
// that one of denies gives.
func namedByDeny(segment string, denies []string) bool {
	return segment == ".duolog" || slices.ContainsFunc(denies, func(deny string) bool {
		name, _ := deniedName(deny)
		matched, _ := doublestar.Match(name, segment)
		return name != "" && matched
	})
}

// inOrder reports whether the characters of literals stand in text in order.
func inOrder(literals []string, text string) bool {
	for _, literal := range literals {
		at := strings.Index(text, literal)
		if at < 0 {
			return false
		}
		text = text[at+len(literal):]
	}
	return true
}

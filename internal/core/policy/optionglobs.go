package policy

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// grepGlobOptions are grep's long options whose value is a glob of file names.
var grepGlobOptions = []string{"--include", "--exclude", "--exclude-dir"}

// grepGlobValue reports whether value, the next word after option word of a
// read command, is a glob that grep takes as a filter.
func grepGlobValue(program, word, value string) bool {
	return program == "grep" && slices.Contains(grepGlobOptions, word) && !strings.Contains(value, "{")
}

// globbedOptionValue reports a next-word option value that the shell can
// expand onto files: it holds a glob or brace and is no filter for grep.
func globbedOptionValue(program, word, value string) bool {
	return !grepGlobValue(program, word, value) && strings.ContainsAny(value, "*?[{")
}

// fileOptionGlob reports whether a grep or rg option that names a file to read
// holds a glob in its own word: the shell can make --file=.e* into --file=.env,
// where the value of --include is only a filter.
func fileOptionGlob(words []string) bool {
	name := strings.ToLower(words[0])
	if name != "grep" && name != "rg" {
		return false
	}
	return slices.ContainsFunc(words[1:], func(word string) bool {
		option, _, _ := strings.Cut(word, "=")
		return strings.HasPrefix(word, "-") && strings.ContainsAny(optionValue(name, word), "*?[") && fileValue(name, option)
	})
}

// judgedTargets is readCommandTargets for a line the gate can check against
// the tree: a file option with a glob gets its reason, a recursive grep that
// filteredGrepReads proves loses its own, and what a Git read takes from the
// top of the repository is listed too.
func judgedTargets(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string) ([]string, string) {
	targets, reason := readCommandTargets(part)
	words := commandWords(part)
	targets = withTopPathspecs(a, words, targets)
	switch {
	case len(words) > 0 && fileOptionGlob(words):
		reason = "a shell glob in an option that names a file can expand onto files that policy denies; name the file"
	case reason == recursiveSearchReason && filteredGrepReads(p, lead, m, a, words, targets):
		reason = ""
	}
	return targets, reason
}

// separateGlobOption reports whether word, an option of a read command, takes
// a glob of file names as the next word.
func separateGlobOption(program, word string) bool {
	if program == "rg" {
		_, separate, ok := rgGlobOption(word)
		return ok && separate
	}
	return program == "grep" && slices.Contains(grepGlobOptions, word)
}

// globStaysOneWord reports whether a glob written as an option's own value
// stays one word in the working folder: with no match the shell passes it as
// written, and with one match that name is the option's value. With more, the
// names after the first are a pattern and files to read, and a word that was
// the pattern becomes a file: rg -g *.go .env reads .env when two names match.
// Go matches a leading dot where the shell does not, so it counts more matches
// than the shell finds, never fewer.
func globStaysOneWord(a Action, word string, budget *globBudget) bool {
	folder := a.Cwd
	if folder == "" {
		folder = a.Work
	}
	if folder == "" || strings.ContainsAny(word, "{~") || globOutside(a, word, budget) {
		return false
	}
	pattern := pathutil.Native(bracketsAsAny(word))
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(folder, pattern)
	}
	matches, ok := budget.glob(pattern)
	return ok && len(matches) <= 1
}

// grepGlobValues reads a grep command's arguments as readCommandTargets does.
// found says whether an option of grepGlobOptions is among them, and values
// are those that readCommandTargets lists as targets: written in the option's
// own word, with no glob. A value written as the next word is listed nowhere,
// and the word after -e or the like is no option at all.
func grepGlobValues(args []string) (values []string, found bool) {
	for i := 0; i < len(args) && args[i] != "--"; i++ {
		name, value, attached := strings.Cut(args[i], "=")
		glob := slices.Contains(grepGlobOptions, name)
		found = found || glob
		switch {
		case separateValue("grep", args[i]):
			i++
		case glob && attached && !strings.ContainsAny(value, "*?[{"):
			values = append(values, value)
		}
	}
	return values, found
}

// withoutOptionValues returns the roots of a search without the values that
// grepGlobValues found, which readCommandTargets lists after the operands, so
// each goes from the end: a value such as the .cache of --exclude-dir=.cache
// is no root, and an operand of the same name stays one.
func withoutOptionValues(roots, values []string) []string {
	roots = slices.Clone(roots)
	for _, value := range values {
		for i := len(roots) - 1; i >= 0; i-- {
			if roots[i] == value {
				roots = slices.Delete(roots, i, i+1)
				break
			}
		}
	}
	return roots
}

// filteredGrepReads reports whether a recursive grep with a glob option of
// grepGlobOptions can read only what the read rules allow, as the tree stands.
// words are the command's, its program at words[0], and roots what it
// searches. Every file under the roots is judged, and no option narrows that:
// BSD grep also matches --include against the path, where * crosses a slash,
// and an expansion can change what --exclude and --exclude-dir leave out, as
// --exclude=.e* no longer hides .env once a file named --exclude=.example is
// there. A symlink, which grep -R follows, or a tree too large for the budget
// proves nothing, and the search asks as any recursive search does.
func filteredGrepReads(p config.Policy, lead LeadRules, m mode.Mode, a Action, words, roots []string) bool {
	values, found := grepGlobValues(words[1:])
	roots = withoutOptionValues(roots, values)
	if words[0] != "grep" || !found || len(roots) == 0 || a.Work == "" {
		return false
	}
	budget := newGlobBudget()
	return !slices.ContainsFunc(roots, func(root string) bool {
		return strings.ContainsAny(root, "*?[{~") || outsideOperand(a, root) || !rootReadsAllowed(p, lead, m, a, root, budget)
	})
}

// rootReadsAllowed walks one search root and judges every file below it.
func rootReadsAllowed(p config.Policy, lead LeadRules, m mode.Mode, a Action, root string, budget *globBudget) bool {
	start := pathutil.Native(root)
	if !filepath.IsAbs(start) {
		start = filepath.Join(workingFolder(a), start)
	}
	err := filepath.WalkDir(start, func(name string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case !budget.spend(1):
			return fs.ErrInvalid
		case specialType(entry.Type()):
			return fs.ErrPermission
		case entry.IsDir():
			return nil
		}
		read := Action{Kind: "read", Target: RelativeTarget(a.Work, a.Cwd, name), Role: a.Role, Shell: a.Shell}
		if Evaluate(p, lead, m, read).Kind != Allow {
			return fs.ErrPermission
		}
		return nil
	})
	return err == nil
}

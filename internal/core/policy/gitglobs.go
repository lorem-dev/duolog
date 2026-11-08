package policy

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// readDenyPatterns returns the patterns, in lower case with forward slashes, of
// the read denies that apply to a's role, from the policy and from the lead.
func readDenyPatterns(p config.Policy, lead LeadRules, a Action) []string {
	rules := append(slices.Clip(lead.Deny), slices.DeleteFunc(slices.Clone(p.Rules), func(rule config.Rule) bool { return rule.Decision != Deny })...)
	var patterns []string
	for _, rule := range rules {
		if rule.Action != "read" || !roleApplies(rule, a.Role) {
			continue
		}
		for _, pattern := range expandBraces(pathutil.Slashes(rule.Match)) {
			patterns = append(patterns, strings.ToLower(pattern))
		}
	}
	return patterns
}

// globAimedAtDeny reports whether a Git pathspec glob is aimed at a name that
// one of denies, the deny patterns of readDenyPatterns, gives. Any wide glob
// may cover a denied file, and that is for the lead to judge: src/*, se*/a.go,
// a.* and *.go ask. Aimed means that the glob's own literal text spells enough
// of the denied name (literalsNeeded), in the place where that name stands: its
// last segment on a file name, as a.p?m on *.pem, or an earlier segment on a
// folder name, as sec*/x on secrets/**. With strict, a dot and one letter of an
// extension, as a.p* has, are not enough.
func globAimedAtDeny(glob string, denies []string, strict bool) bool {
	for _, pattern := range expandBraces(strings.ToLower(pathutil.Slashes(glob))) {
		segments := strings.Split(pattern, "/")
		if slices.ContainsFunc(denies, func(deny string) bool { return segmentsMeetDeny(segments, deny, strict) }) {
			return true
		}
	}
	return false
}

// segmentsMeetDeny reports whether a segment of a glob, in the place the deny
// pattern's name stands, meets that name and spells enough of it.
func segmentsMeetDeny(segments []string, deny string, strict bool) bool {
	name, folder := deniedName(deny)
	if name == "" {
		return false
	}
	denied := globTokens(name)
	places := segments[len(segments)-1:]
	if folder {
		places = segments[:len(segments)-1]
	}
	needed := literalsNeeded(denied)
	if strict {
		needed = max(needed, min(3, literalCount(denied)))
	}
	return slices.ContainsFunc(places, func(segment string) bool { return tokensMeet(globTokens(segment), denied) >= needed })
}

// slashVariants returns a pathspec glob and its spellings with a slash where a
// wildcard stands, since Git's * and ? match a slash outside :(glob): one
// wildcard at a time, and all of them at once. secrets* can name secrets/x as
// secrets*/*, and sub?.env can name sub/.env.
func slashVariants(glob string) []string {
	variants := []string{glob}
	crossing := map[byte]string{'*': "*/*", '?': "/"}
	var all strings.Builder
	for i := 0; i < len(glob); i++ {
		if glob[i] == '[' {
			if end := bracketEnd(glob, i); end > 0 {
				all.WriteString(glob[i : end+1])
				i = end
				continue
			}
		}
		slash, wild := crossing[glob[i]]
		if !wild {
			all.WriteByte(glob[i])
			continue
		}
		all.WriteString(slash)
		variants = append(variants, glob[:i]+slash+glob[i+1:])
	}
	if len(variants) > 1 {
		variants = append(variants, all.String())
	}
	return variants
}

// literalsNeeded is how many literal characters of a denied name a glob must
// spell to be aimed at it: the dot and a letter of an extension such as *.pem,
// as a.p* has, and otherwise three, as sec*/x and .en? have, or all of a shorter
// name. A letter or two of a folder name say nothing: se*/a.go is ordinary.
func literalsNeeded(denied []string) int {
	needed := 3
	if len(denied) > 1 && denied[0] == "*" && denied[1] == "." {
		needed = 2
	}
	return max(1, min(needed, literalCount(denied)))
}

// literalCount is how many tokens are one literal character.
func literalCount(tokens []string) int {
	return len(slices.DeleteFunc(slices.Clone(tokens), func(token string) bool { return !literalToken(token) }))
}

// literalToken reports whether a token is one literal character.
func literalToken(token string) bool {
	return token != "*" && token != "?" && utf8.RuneCountInString(token) == 1
}

// deniedName returns the last segment of a deny pattern that names something,
// and whether it is a folder, so that what the pattern denies lies below it.
func deniedName(deny string) (name string, folder bool) {
	trimmed := strings.TrimRight(deny, "/")
	folder = trimmed != deny
	segments := strings.Split(trimmed, "/")
	for len(segments) > 0 && !namesSomething(globTokens(segments[len(segments)-1])) {
		segments = segments[:len(segments)-1]
		folder = true
	}
	if len(segments) == 0 {
		return "", false
	}
	return segments[len(segments)-1], folder
}

// globTokens splits one path segment of a glob into its characters, each [...]
// class whole, with a run of * as one *.
func globTokens(segment string) []string {
	runes := []rune(segment)
	var tokens []string
	for i := 0; i < len(runes); i++ {
		token := string(runes[i])
		if runes[i] == '[' {
			token, i = classToken(runes, i)
		}
		if token != "*" || len(tokens) == 0 || tokens[len(tokens)-1] != "*" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// classToken returns the token of the [...] class that opens at runes[i] and
// the index of its last character: a [ that nothing closes is that character,
// and a class of one plain character is that literal character, as [s] is s.
func classToken(runes []rune, i int) (string, int) {
	// A ] right after the [ belongs to the class.
	from := min(i+2, len(runes))
	end := slices.Index(runes[from:], ']')
	if end < 0 {
		return "[", i
	}
	end += from
	if end == i+2 && !strings.ContainsRune("!^*?", runes[i+1]) {
		return string(runes[i+1]), end
	}
	return string(runes[i : end+1]), end
}

// namesSomething reports whether tokens hold more than wildcards.
func namesSomething(tokens []string) bool {
	return slices.ContainsFunc(tokens, func(token string) bool { return token != "*" && token != "?" })
}

// tokensMeet returns how many literal characters of a denied name a segment of
// a glob spells, both as tokens, at best, or -1 when they cannot be the same
// text with the glob's * standing only for what starCovers lets it. It fills a
// table from the end, so that a long glob costs little.
func tokensMeet(glob, denied []string) int {
	meet := make([][]int, len(glob)+1)
	for i := range meet {
		meet[i] = slices.Repeat([]int{-1}, len(denied)+1)
	}
	meet[len(glob)][len(denied)] = 0
	for i := len(glob); i >= 0; i-- {
		for j := len(denied); j >= 0; j-- {
			meet[i][j] = max(meet[i][j], stepMeets(glob, denied, meet, i, j))
		}
	}
	return meet[0][0]
}

// stepMeets is one cell of tokensMeet's table: the literal characters that
// glob[i:] spells of denied[j:], or -1, given the cells after it.
func stepMeets(glob, denied []string, meet [][]int, i, j int) int {
	best := -1
	deniedStar := j < len(denied) && denied[j] == "*"
	if deniedStar {
		best = meet[i][j+1]
	}
	if i == len(glob) {
		return best
	}
	if deniedStar {
		best = max(best, meet[i+1][j])
	}
	switch {
	case glob[i] == "*":
		return max(best, starCovers(denied, meet[i+1], j))
	case j == len(denied) || deniedStar || !tokenMeets(glob[i], denied[j]) || meet[i+1][j+1] < 0:
		return best
	case literalToken(glob[i]) && literalToken(denied[j]):
		return max(best, meet[i+1][j+1]+1)
	}
	return max(best, meet[i+1][j+1])
}

// starCovers returns the best of rest over the k for which a * of the glob can
// stand for denied[j:k]. It may stand for letters or for one dot, as in *env
// for the dot of .env, but not for a dotted part: *.go is not aimed at .env.go,
// nor se* at se.pem.
func starCovers(denied []string, rest []int, j int) int {
	best := -1
	for k := j; k <= len(denied); k++ {
		if k-j > 1 && slices.Contains(denied[j:k], ".") {
			break
		}
		best = max(best, rest[k])
	}
	return best
}

// tokenMeets reports whether two tokens other than * can be the same character.
func tokenMeets(glob, denied string) bool {
	globClass, deniedClass := len(glob) > 1 && glob[0] == '[', len(denied) > 1 && denied[0] == '['
	switch {
	case glob == "?" || denied == "?" || globClass && deniedClass:
		return true
	case globClass:
		ok, _ := doublestar.Match(glob, denied)
		return ok
	case deniedClass:
		ok, _ := doublestar.Match(denied, glob)
		return ok
	}
	return glob == denied
}

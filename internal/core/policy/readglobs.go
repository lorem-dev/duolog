package policy

import (
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

func readGlobMatchesDeny(p config.Policy, lead LeadRules, a Action, globs []string) bool {
	var probes []string
	add := func(rule config.Rule) {
		if rule.Action != "read" || !roleApplies(rule, a.Role) {
			return
		}
		for _, pattern := range expandBraces(pathutil.Slashes(rule.Match)) {
			parts := strings.Split(strings.TrimRight(pattern, "/"), "/")
			for len(parts) > 1 && parts[len(parts)-1] == "**" {
				parts = parts[:len(parts)-1]
			}
			name := strings.ReplaceAll(parts[len(parts)-1], "**", "")
			probes = append(probes, strings.ToLower(strings.ReplaceAll(name, "*", "x")))
		}
	}
	for _, rule := range p.Rules {
		if rule.Decision == Deny {
			add(rule)
		}
	}
	for _, rule := range lead.Deny {
		add(rule)
	}
	for _, glob := range globs {
		if strings.HasPrefix(glob, "!") {
			continue
		}
		for _, pattern := range expandBraces(strings.ToLower(pathutil.Slashes(glob))) {
			for _, probe := range probes {
				if ok, _ := doublestar.Match(pattern, probe); ok {
					return true
				}
				if ok, _ := doublestar.Match(globLastSegment(pattern), probe); ok {
					return true
				}
			}
		}
	}
	return false
}

// globLastSegment keeps the directory name when a glob selects its contents.
func globLastSegment(pattern string) string {
	pattern = strings.TrimRight(pattern, "/")
	pattern = strings.TrimRight(strings.TrimSuffix(pattern, "/**"), "/")
	return pattern[strings.LastIndex(pattern, "/")+1:]
}

// unquotedRgGlob reports whether an rg glob option of a's line is written so
// that the shell can turn it into something else.
func unquotedRgGlob(a Action) bool {
	// Mask quoted option values before looking for shell expansions.
	masked := quotedRgGlob.ReplaceAllString(a.Target, "${1}literal")
	parts, _ := readShellParts(masked, a.Shell)
	budget := newGlobBudget()
	return slices.ContainsFunc(parts, func(part string) bool {
		words := commandWords(part)
		return len(words) > 0 && words[0] == "rg" && rgGlobsMoveWords(a, words[1:], budget)
	})
}

// rgGlobsMoveWords reports whether a glob option among an rg command's
// arguments can change which word is what. A value in the option's own word
// cannot, unless a glob stands before it: the shell keeps -g or --glob= and
// can only fill in a file's name after it. A brace makes more words of it, and
// a value written apart must stay one word (globStaysOneWord).
func rgGlobsMoveWords(a Action, args []string, budget *globBudget) bool {
	for i := 0; i < len(args) && args[i] != "--"; i++ {
		value, separate, ok := rgGlobOption(args[i])
		switch {
		case ok && separate:
			i++
			if i < len(args) && strings.ContainsAny(args[i], "*?[{") && !globStaysOneWord(a, args[i], budget) {
				return true
			}
		case ok:
			if strings.Contains(value, "{") || strings.ContainsAny(args[i][:len(args[i])-len(value)], "*?[") {
				return true
			}
		case separateValue("rg", args[i]):
			i++
		}
	}
	return false
}

package gate

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/pathutil"
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

func unquotedRgGlob(command, shell string) bool {
	// Mask quoted option values before looking for shell expansions.
	masked := quotedRgGlob.ReplaceAllString(command, "${1}literal")
	parts, _ := ShellParts(masked, shell)
	for _, part := range parts {
		words := strings.Fields(part)
		if len(words) == 0 || words[0] != "rg" {
			continue
		}
		for _, glob := range rgGlobs(words[1:]) {
			if strings.ContainsAny(glob, "*?[{") {
				return true
			}
		}
	}
	return false
}

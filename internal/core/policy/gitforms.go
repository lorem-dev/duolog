package policy

import (
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// gitTargets lists what a Git read names. A word may be a revision instead of a
// path, which read rules allow, and rev:path reads that path from the history.
// git log -L prints lines of the file it names, and a pathspec may carry magic
// in front of its path. git diff and git log with no path still show tracked
// denied files: like plain rg, that is a search the gate cannot see into.
func gitTargets(args []string) ([]string, string) {
	if len(args) == 0 {
		return nil, ""
	}
	var targets []string
	literal := false
	for i := 1; i < len(args); i++ {
		word := args[i]
		switch {
		case literal:
			targets = append(targets, pathspecPaths(word)...)
		case word == "--":
			literal = true
		case gitPatternOption(word):
			// The next word is a pattern, not a path. A bare --pretty takes
			// none, and --format and --pretty take theirs only after =.
			i++
		case word == "-L" && i+1 < len(args):
			i++
			targets = append(targets, lineRangeFile(args[i])...)
		case strings.HasPrefix(word, "-L"):
			targets = append(targets, lineRangeFile(word[2:])...)
		case strings.HasPrefix(word, "-"):
		default:
			targets = append(targets, gitOperandPaths(word)...)
		}
	}
	targets, reason := checkTargets(targets)
	if reason == "" && slices.ContainsFunc(args[1:], icasePathspec) {
		reason = "a case-insensitive pathspec can match a denied file under another case; name the file"
	}
	return targets, reason
}

// gitOperandPaths returns the paths of an operand before --: a pathspec with
// magic, rev:path's path, or the word itself.
func gitOperandPaths(word string) []string {
	if strings.HasPrefix(word, ":") {
		return pathspecPaths(word)
	}
	if _, path, found := strings.Cut(word, ":"); found && !pathutil.IsAbs(word) {
		return []string{path}
	}
	return []string{word}
}

// lineRangeFile returns the file of a value of git log -L, as Git reads it:
// what follows the range. A function name ends at the next colon, and a colon
// inside a /regex/ of the range is the regex's. A leading ^ only starts the
// search for the function or regex at the top of the file.
func lineRangeFile(value string) []string {
	value = strings.TrimPrefix(value, "^")
	start := 0
	if strings.HasPrefix(value, ":") {
		start = 1
	}
	regex := false
	for i := start; i < len(value); i++ {
		switch c := value[i]; {
		case c == '\\':
			i++
		case c == '/' && start == 0:
			regex = !regex
		case c == ':' && !regex:
			if file := value[i+1:]; file != "" {
				return []string{file}
			}
			return nil
		}
	}
	return nil
}

// gitRangeFile returns the file of a word of git's -L whose range is plain, a
// line number or :name: with or without ^, and "" for any other word. The
// colons of such a range are no stream name, and what Git reads is the file
// after it. A value that starts like a path is left to be judged whole.
func gitRangeFile(program, word string) string {
	value, attached := strings.CutPrefix(word, "-L")
	plain := strings.TrimPrefix(value, "^")
	if program != "git" || !attached || plain == "" || plain[0] != ':' && (plain[0] < '0' || plain[0] > '9') {
		return ""
	}
	return strings.Join(lineRangeFile(value), "")
}

// parsePathspec reads a Git pathspec that starts with a colon: the long form
// :(top,icase)path, the short form :/path or :!path, or the index stage of
// :0:path. excluded is true for a path that the pathspec takes out.
func parsePathspec(word string) (path string, excluded, icase bool) {
	rest := word[1:]
	if len(rest) >= 2 && rest[0] >= '0' && rest[0] <= '3' && rest[1] == ':' {
		return rest[2:], false, false
	}
	if strings.HasPrefix(rest, "(") {
		magic, path, closed := strings.Cut(rest[1:], ")")
		if !closed {
			return word, false, false
		}
		names := strings.Split(magic, ",")
		return path, slices.Contains(names, "exclude"), slices.Contains(names, "icase")
	}
	for rest != "" && strings.IndexByte("/!^", rest[0]) >= 0 {
		excluded = excluded || rest[0] != '/'
		rest = rest[1:]
	}
	return strings.TrimPrefix(rest, ":"), excluded, false
}

// pathspecPaths returns the paths that a pathspec reads, to judge as read
// targets: none for one that only excludes, and the lower-case spelling too
// for a case-insensitive one, as the deny rules are written.
func pathspecPaths(word string) []string {
	if !strings.HasPrefix(word, ":") {
		return []string{word}
	}
	path, excluded, icase := parsePathspec(word)
	switch {
	case excluded || path == "":
		return nil
	case icase:
		return []string{path, strings.ToLower(path)}
	}
	return []string{path}
}

// icasePathspec reports whether word is a case-insensitive pathspec.
func icasePathspec(word string) bool {
	if !strings.HasPrefix(word, ":") {
		return false
	}
	_, excluded, icase := parsePathspec(word)
	return icase && !excluded
}

// gitGlobDenied reports whether a Git read among parts names a pathspec glob
// aimed at a name policy denies (globAimedAtDeny). Git expands *, ? and [ of a
// pathspec itself, so quoting one does not help, and git log -p prints what it
// matches. Any other glob is left to ask, or to the state rule.
func gitGlobDenied(p config.Policy, lead LeadRules, a Action, parts []string) bool {
	denies := readDenyPatterns(p, lead, a)
	return slices.ContainsFunc(parts, func(part string) bool {
		words := commandWords(part)
		if len(words) < 2 || !strings.EqualFold(words[0], "git") || !readOnlyCommand(part) {
			return false
		}
		targets, _ := gitTargets(words[1:])
		return slices.ContainsFunc(targets, func(target string) bool {
			return strings.ContainsAny(target, "*?[") && globAimedAtDeny(target, denies)
		})
	})
}

// gitOptionGlob reports whether a Git read of a POSIX line has a word that
// starts with - and holds a *, ? or [ outside quotes. The shell makes options
// of the files such a word matches, and a file named -L1,1:.env or
// --output=x is enough to read a denied file or to write one.
func gitOptionGlob(a Action) bool {
	return slices.ContainsFunc(readCommandWords(a), func(words []shellWord) bool {
		return programName(words[0].text) == "git" && slices.ContainsFunc(words[1:], func(word shellWord) bool {
			return word.hasGlob() && strings.HasPrefix(word.text, "-")
		})
	})
}

// gitOptionGlobDeny is the deny of a Git read with such a word where only reads
// the gate can judge may run: in plan and for a read-only role, as the same
// word on grep is denied there. Elsewhere it asks, through readGlobWords.
func gitOptionGlobDeny(p config.Policy, lead LeadRules, a Action, m mode.Mode) *Decision {
	if !gitOptionGlob(a) {
		return nil
	}
	if lead.RoleSandbox == "read-only" || !roleCanWrite(p, Action{Kind: "write", Target: "role-check", Role: a.Role}) {
		return &Decision{Kind: Deny, Reason: "read-only role requires a named read-only command", Rule: "role sandbox", Mode: m}
	}
	if m == mode.Plan {
		return &Decision{Kind: Deny, Reason: "plan mode permits only reads", Rule: "mode plan", Mode: m}
	}
	return nil
}

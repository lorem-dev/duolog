package gate

import (
	"runtime"
	"slices"
	"strings"
)

// CommandWordLists returns, for a plain POSIX line that lexShell reads, the
// word lists in which a check for a command such as duolog telemetry looks:
// each command's words, and the pieces of each word split at whitespace, at
// shell separators, quotes and =, since a word may be a script for sh -c,
// eval, git -c core.editor= and the like. A message program's quoted text
// with whitespace in it is prose and is left out: words in a commit message
// are not commands. ok is false for any other line, which the caller then
// reads coarsely.
func CommandWordLists(command, shell string) ([][]string, bool) {
	if runtime.GOOS == "windows" || shell == "powershell" || shell == "windows" {
		return nil, false
	}
	segments, _, ok := lexShell(command)
	if !ok {
		return nil, false
	}
	var lists [][]string
	for _, segment := range segments {
		words, _, ok := splitRedirects(segment)
		if !ok {
			return nil, false
		}
		words = afterKeywords(words)
		texts := make([]string, 0, len(words))
		for _, word := range words {
			texts = append(texts, word.text)
		}
		if messageProgram(texts) {
			texts = slices.DeleteFunc(texts, func(text string) bool { return strings.ContainsAny(text, " \t\n") })
			lists = append(lists, texts)
			continue
		}
		lists = append(lists, texts)
		for _, text := range texts {
			if pieces := strings.FieldsFunc(text, scriptSeparator); len(pieces) > 1 {
				lists = append(lists, pieces)
			}
		}
	}
	return lists, true
}

func scriptSeparator(r rune) bool {
	return strings.ContainsRune(" \t\r\n;|&(){}<>'\"`=", r)
}

// messageProgram reports whether a command only takes its quoted words as
// text: echo, printf, git commit and git tag with no option before the
// subcommand, and gh pr and gh issue. Only assignments may stand in front: a
// wrapper such as watch or sh -c runs its words through a shell again.
func messageProgram(words []string) bool {
	start := execOperandStart(words)
	if start <= 0 || start > len(words) {
		return false
	}
	if slices.ContainsFunc(words[:start-1], func(w string) bool { return !execAssignment.MatchString(w) }) {
		return false
	}
	var sub string
	if start < len(words) {
		sub = words[start]
	}
	switch programName(words[start-1]) {
	case "echo", "printf":
		return true
	case "git":
		return sub == "commit" || sub == "tag"
	case "gh":
		return sub == "pr" || sub == "issue"
	}
	return false
}

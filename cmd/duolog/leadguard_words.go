package main

import (
	"strings"

	"github.com/lorem-dev/duolog/internal/core/policy"
)

// peerShowWords splits command text into words, with quotes removed and
// shell operators taken as spaces, so that duolog inside sh -c '...', $(...)
// or a && list is a word of its own.
var peerShowWords = strings.NewReplacer("'", "", `"`, "", "`", "", ";", " ", "&", " ", "|", " ", "(", " ", ")", " ", "<", " ", ">", " ", "$", " ", "{", " ", "}", " ")

// namesPeerShow reports whether text holds duolog [flags] peer [flags] show.
func namesPeerShow(text string) bool {
	return namesDuologSub(text, "peer", "show")
}

// namesRunsTail reports whether text holds duolog [flags] runs [flags] tail.
func namesRunsTail(text string) bool {
	return namesDuologSub(text, "runs", "tail")
}

// namesDuologSub reports whether text holds duolog [flags] command [flags]
// sub.
func namesDuologSub(text, command, sub string) bool {
	words := strings.Fields(peerShowWords.Replace(text))
	for i, w := range words {
		if policy.ProgramName(w) != "duolog" {
			continue
		}
		j := skipDuologFlags(words, i+1)
		if j >= len(words) || words[j] != command {
			continue
		}
		if j = skipDuologFlags(words, j+1); j < len(words) && words[j] == sub {
			return true
		}
	}
	return false
}

// skipDuologFlags returns the index of the first word from j that is not a
// flag; --lead takes the next word.
func skipDuologFlags(words []string, j int) int {
	for j < len(words) && strings.HasPrefix(words[j], "-") {
		if words[j] == "--lead" {
			j++
		}
		j++
	}
	return j
}

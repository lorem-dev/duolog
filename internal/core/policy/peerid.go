package policy

import "strings"

// RulePeerID names the allow for a peer that reads its own id.
const RulePeerID = "peer id"

// peerAllow is the reason and rule when a peer may do the action whatever its
// role and mode: write its own report (report), or read its own id. The rule
// is "" otherwise.
func peerAllow(a Action, report bool) (reason, rule string) {
	switch {
	case report:
		return "the peer's own report file", RuleReport
	case ownPeerIDRead(a):
		return "the peer reads its own id", RulePeerID
	}
	return "", ""
}

// ownPeerIDRead reports whether the shell line is only a print of the peer's
// own DUOLOG_PEER: echo of the variable, or printenv of that one name. It
// reveals what the peer's prompt already says and nothing else from the
// environment. Any other word, redirect or second command keeps the line out,
// and only a blank or a tab separates the words: a newline would end the
// command, and printenv alone prints every variable.
func ownPeerIDRead(a Action) bool {
	if a.Kind != "exec" || a.Peer == "" {
		return false
	}
	blank := func(r rune) bool { return r == ' ' || r == '\t' }
	switch strings.Join(strings.FieldsFunc(a.Target, blank), " ") {
	case `echo $DUOLOG_PEER`, `echo "$DUOLOG_PEER"`, `echo ${DUOLOG_PEER}`, `echo "${DUOLOG_PEER}"`,
		`printenv DUOLOG_PEER`:
		return true
	}
	return false
}

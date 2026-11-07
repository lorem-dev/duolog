package policy

import (
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// planLimit is the hard limit of level plan for any action but a read, and
// true when it decides. Evaluate applies it before every return that asks: an
// ask is answered by an allow, which would run the call. Only a command
// proved to be permitted reads passes. An uncertain line whose every part is
// a known read-only command, such as rg with a backslash in its pattern, is
// asked, with readAsk when the read rules ask; any other uncertain one is
// denied, not asked. readOnlyCommand judges the program and its options only,
// so a line that can write (lineWrites), or with a backtick, a > or a (
// anywhere, is denied too.
func planLimit(p config.Policy, m mode.Mode, a Action, parts []string, uncertain, lineWrites bool, readAsk *Decision, decision func(kind, reason, rule string) Decision) (Decision, bool) {
	if m != mode.Plan || a.Kind == "read" {
		return Decision{}, false
	}
	if a.Kind == "exec" && namedReads(p, a, parts, uncertain) {
		return Decision{}, false
	}
	if !uncertainRead(a, parts, uncertain, lineWrites) {
		return decision(Deny, "plan mode permits only reads", "mode plan"), true
	}
	if readAsk != nil {
		return *readAsk, true
	}
	return decision(Ask, "plan mode: a read command whose shell syntax duolog cannot read for sure requires approval", "mode plan"), true
}

// namedReads reports whether a command line is certain and every part of it
// is a read-only command that a read-only allow rule names: all that a
// read-only role may run.
func namedReads(p config.Policy, a Action, parts []string, uncertain bool) bool {
	if uncertain || len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !allowedReadCommand(p, a.Role, part, a.Shell) {
			return false
		}
	}
	return true
}

// uncertainRead reports whether an uncertain exec line holds only known
// read-only commands, with no (, backtick or > that could run or write
// something else.
func uncertainRead(a Action, parts []string, uncertain, lineWrites bool) bool {
	if a.Kind != "exec" || !uncertain || lineWrites || len(parts) == 0 {
		return false
	}
	// A ( anywhere covers $( and <( substitutions, subshells, extended globs
	// and zsh glob qualifiers such as *(e:'cmd':), which run cmd while the
	// shell expands the glob.
	if strings.ContainsAny(a.Target, "`>(") {
		return false
	}
	for _, part := range parts {
		if !readOnlyCommand(part) {
			return false
		}
	}
	return true
}

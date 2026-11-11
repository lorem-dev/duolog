package policy

import (
	"regexp"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// plainAssignment is a shell variable assignment with a plain value: no
// quote, escape, expansion, substitution or operator.
var plainAssignment = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=([^$` + "`" + `()<>;|&'"\\\s]*)$`)

// envOrOutsideAllow is namedEnvAllow, then outsideReadAllow, for Evaluate.
func envOrOutsideAllow(p config.Policy, lead LeadRules, m mode.Mode, a Action, readAsk *Decision, decision func(kind, reason, rule string) Decision) (Decision, bool) {
	if d, ok := namedEnvAllow(p, lead, m, a, decision); ok {
		return d, true
	}
	return outsideReadAllow(p, lead, a, readAsk, decision)
}

// namedEnvAllow allows a command that sets variables in front of it, such as
// UE_ROOT=/opt/engine task ue:build, when an allow rule of the project policy
// matches the whole line and names each of its variables at its start, as
// "UE_ROOT=* task ue:*" does. A variable can change what a program runs, as
// LD_PRELOAD or GOFLAGS can, so the gate takes none on its own: the rule is
// the owner's choice of these names. The command after the assignments must
// still pass every deny; ok is false when no such rule matches.
func namedEnvAllow(p config.Policy, lead LeadRules, m mode.Mode, a Action, decision func(kind, reason, rule string) Decision) (Decision, bool) {
	if a.Kind != "exec" || a.Shell == "powershell" || m != mode.Edits && m != mode.Full {
		return Decision{}, false
	}
	names, command, ok := leadingAssignments(a.Target, a.Shell)
	if !ok {
		return Decision{}, false
	}
	rest := a
	rest.Target = command
	for _, rule := range p.Rules {
		if rule.Decision != Allow || !namesVariables(rule.Match, names) || !matches(rule, a) {
			continue
		}
		if d := Evaluate(p, lead, m, rest); d.Kind == Deny {
			return d, true
		}
		return decision(Allow, "allowed by a policy rule that names its variables", rule.Action+" "+rule.Match), true
	}
	return Decision{}, false
}

// leadingAssignments splits a line into the names of the plain assignments
// in front of it and the command after them. ok is false without an
// assignment, without a command, or when the command is more than one plain
// command: a * of a rule must not take in a second one after ; or &&, nor a
// substitution or redirect.
func leadingAssignments(line, shell string) (names []string, command string, ok bool) {
	words := strings.Fields(line)
	for _, w := range words {
		match := plainAssignment.FindStringSubmatch(w)
		if match == nil {
			break
		}
		names = append(names, match[1])
	}
	if len(names) == 0 || len(names) == len(words) {
		return nil, "", false
	}
	command = strings.Join(words[len(names):], " ")
	if parts, uncertain := readShellParts(command, shell); uncertain || len(parts) != 1 || strings.ContainsAny(command, "$`()<>;|&\\'\"") {
		return nil, "", false
	}
	return names, command, true
}

// namesVariables reports whether every brace form of a rule's match starts
// with an assignment of each of names, in order, as UE_ROOT=* does.
func namesVariables(match string, names []string) bool {
	for _, pattern := range expandBraces(match) {
		words := strings.Fields(pattern)
		if len(words) <= len(names) {
			return false
		}
		for i, name := range names {
			if !strings.HasPrefix(words[i], name+"=") {
				return false
			}
		}
	}
	return true
}

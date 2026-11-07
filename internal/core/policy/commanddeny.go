package policy

import (
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// denyRules are the lead's denies and the policy's deny rules.
func denyRules(p config.Policy, lead LeadRules) []config.Rule {
	denies := slices.Clip(lead.Deny)
	for _, rule := range p.Rules {
		if rule.Decision == Deny {
			denies = append(denies, rule)
		}
	}
	return denies
}

// commandDeny holds the agent rules (agentRule) for each part as commandParts
// reads it, also for a command that another command's words may start
// (innerCommandDeny). A peer's report skips the policy's rules, as in
// ruleDeny. Every other rule judges the parts as written.
func commandDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, parts []string, report bool, decision func(kind, reason, rule string) Decision) (Decision, bool) {
	for _, part := range parts {
		if d, ok := ruleDeny(p, lead, a, part, report, true, decision); ok {
			return d, true
		}
		if a.Kind == "exec" && !readOnlyCommand(part) {
			if d, ok := innerCommandDeny(p, lead, m, a, part, true); ok {
				return d, true
			}
		}
	}
	return Decision{}, false
}

// ruleDeny holds the lead's denies and the policy's deny rules for one part,
// by its words and by its canonical command (denyCommand), so that a path
// deny and a basename command deny both hold. agent picks the agent rules
// (agentRule) or every other rule. A peer's report skips the policy's rules.
func ruleDeny(p config.Policy, lead LeadRules, a Action, part string, report, agent bool, decision func(kind, reason, rule string) Decision) (Decision, bool) {
	partAction := a
	partAction.Target = part
	baseAction := partAction
	if a.Kind == "exec" {
		baseAction.Target = denyCommand(part)
		if a.Shell == "powershell" {
			baseAction.Target = denyPowerShell(part)
		}
	}
	for _, rule := range lead.Deny {
		if agentRule(rule) == agent && (matches(rule, partAction) || matches(rule, baseAction)) {
			return decision(Deny, "denied by lead settings", "lead: "+rule.Action+" "+rule.Match), true
		}
	}
	for _, rule := range p.Rules {
		if rule.Decision == Deny && !report && agentRule(rule) == agent && (matches(rule, partAction) || matches(rule, baseAction)) {
			return decision(Deny, "denied by project policy", rule.Action+" "+rule.Match), true
		}
	}
	return Decision{}, false
}

// innerCommandDeny holds the exec denies for every command that the words of
// one command may start: each run of words up to the end, as in uv run git
// push. A run may start inside a substitution or a subshell, as in $(git
// push), whose opening the parser left on the word when a backslash and a
// line end split $ from (. agent picks the agent rules (agentRule) or every
// other exec deny.
func innerCommandDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string, agent bool) (Decision, bool) {
	words := strings.Fields(part)
	for i := range words {
		tail := strings.TrimLeft(strings.Join(words[i:], " "), "$(`<>")
		for _, rule := range denyRules(p, lead) {
			if rule.Action == "exec" && agentRule(rule) == agent && roleApplies(rule, a.Role) && denyMatchesCommand(rule.Match, tail) {
				return Decision{Kind: Deny, Reason: "denied command inside this command", Rule: rule.Action + " " + rule.Match, Mode: m}, true
			}
		}
	}
	return Decision{}, false
}

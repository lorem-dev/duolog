package gate

import (
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// partDecision is what the policy rules decide for one part of a command:
// the first rule that matches it, or on_unmatched. Full mode may confirm an
// ask; see confirmInFull. decision is Evaluate's, which marks an exec ask
// that names an outside path for the person.
func partDecision(p config.Policy, m mode.Mode, a Action, part string, decision func(kind, reason, rule string) Decision, wildOutside func(config.Rule, string) bool) Decision {
	partAction := a
	partAction.Target = part
	var d Decision
	if rule, ok := firstRule(p, a, partAction); ok {
		d = decision(rule.Decision, "matched project policy", rule.Action+" "+rule.Match)
		if wildOutside(rule, part) {
			d = decision(Ask, "a wildcard rule does not allow a path outside the work root", RuleOutside)
		}
	} else {
		d = decision(p.OnUnmatched, "no matching policy rule", "on_unmatched")
		// on_unmatched: allow names no program at all, so it is a wildcard too.
		if d.Kind == Allow && wildOutside(config.Rule{Action: a.Kind, Decision: Allow, Match: "**"}, part) {
			d = decision(Ask, "on_unmatched allow does not allow a path outside the work root", RuleOutside)
		}
	}
	if d.Kind != Allow && d.Kind != Ask && d.Kind != Deny {
		d = decision(Deny, "invalid policy decision", "invalid policy")
	}
	return confirmInFull(m, d)
}

// firstRule returns the first rule that is not a deny and matches one part,
// partAction, of a's command. A read-command wildcard grants reads only; a
// rule naming one exact command stays the project's own choice.
func firstRule(p config.Policy, a, partAction Action) (config.Rule, bool) {
	for _, rule := range p.Rules {
		if rule.Decision == Deny || !matches(rule, partAction) {
			continue
		}
		if a.Kind == "exec" && rule.Decision == Allow && strings.Contains(rule.Match, "*") && readOnlyRule(rule.Match) && !readOnlyCommand(partAction.Target) {
			continue
		}
		return rule, true
	}
	return config.Rule{}, false
}

// wildcardAllowsOutside returns a check for whether a policy rule allows a's
// command only through a wildcard while the command names a path outside the
// work root. Such a rule names programs, not paths, so the person decides.
// A read command that a read-command rule allows is left out: its paths
// passed the read rules, and its patterns are not paths. ExecOutside runs
// once, for the first such rule.
func wildcardAllowsOutside(a Action) func(rule config.Rule, part string) bool {
	known, outside := false, false
	return func(rule config.Rule, part string) bool {
		if a.Kind != "exec" || rule.Decision != Allow || !strings.Contains(rule.Match, "*") ||
			readOnlyRule(rule.Match) && readOnlyCommand(part) {
			return false
		}
		if !known {
			known, outside = true, ExecOutside(a)
		}
		return outside
	}
}

// confirmInFull lets full mode confirm a rule's ask. An ask that needs the
// person stays one: paths outside the work root ask in every mode.
func confirmInFull(m mode.Mode, d Decision) Decision {
	if m != mode.Full || d.Kind != Ask || PersonOnly(d) {
		return d
	}
	return Decision{Kind: Allow, Reason: "full mode confirms action", Rule: "mode full: " + d.Rule, Mode: d.Mode}
}

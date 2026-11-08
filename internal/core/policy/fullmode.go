package policy

import (
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// partDecision is what the policy rules decide for one part of a command:
// the first rule that matches it, or on_unmatched. Full mode may confirm an
// ask; see confirmInFull. decision is Evaluate's, which marks an exec ask
// that names an outside path for the person.
func partDecision(p config.Policy, m mode.Mode, a Action, part string, decision func(kind, reason, rule string) Decision, wildOutside func(config.Rule, string) bool) Decision {
	partAction := a
	partAction.Target = part
	if a.Kind == "exec" && goEnvWrites(part) {
		return decision(Ask, "go env -w and -u change the user's Go env file outside the work root", RuleOutside)
	}
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

// goEnvWrites reports whether any run of a part's words is go env with -w or
// -u, as in go env -w GOFLAGS=x or env go -C dir env --u=GOPROXY. These write
// the user's Go env file, which every later go run reads, the lead's too.
func goEnvWrites(part string) bool {
	words := strings.Fields(part)
	for i, word := range words {
		if programName(word) != "go" {
			continue
		}
		args, ok := goEnvArgs(words[i+1:])
		if ok && slices.ContainsFunc(args, func(arg string) bool {
			arg = strings.Trim(arg, `"'`)
			name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			return strings.HasPrefix(arg, "-") && (name == "w" || name == "u")
		}) {
			return true
		}
	}
	return false
}

// goEnvArgs returns the words after env when the words after go run go env,
// past options such as -C dir before the subcommand.
func goEnvArgs(rest []string) ([]string, bool) {
	for len(rest) > 0 && strings.HasPrefix(strings.Trim(rest[0], `"'`), "-") {
		if option := strings.Trim(rest[0], `"'`); option == "-C" || option == "--C" {
			rest = rest[min(2, len(rest)):]
		} else {
			rest = rest[1:]
		}
	}
	if len(rest) == 0 || strings.Trim(rest[0], `"'`) != "env" {
		return nil, false
	}
	return rest[1:], true
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
// passed the read rules, and its patterns are not paths. A line with a glob
// that the shell expands is not left out (shellGlobInRead), since a pattern
// with a glob can become files. ExecOutside runs once, for the first such
// rule.
func wildcardAllowsOutside(a Action) func(rule config.Rule, part string) bool {
	known, outside := false, false
	return func(rule config.Rule, part string) bool {
		if a.Kind != "exec" || rule.Decision != Allow || !strings.Contains(rule.Match, "*") ||
			readOnlyRule(rule.Match) && readOnlyCommand(part) && !shellGlobInRead(a) {
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

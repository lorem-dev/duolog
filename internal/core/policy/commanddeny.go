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

// operandPrograms never run their operands, so a path after them names a file
// or a package, not a start of an agent. A program with subcommands lists the
// ones that never run an operand; nil means every use.
var operandPrograms = map[string][]string{
	"go":  {"test", "vet", "build", "list", "fmt", "doc", "mod", "env", "version"},
	"git": {"add", "diff", "log", "status", "show", "blame", "rm", "mv", "restore", "ls-files", "grep", "checkout"},
	"ls":  nil, "cat": nil, "grep": nil, "egrep": nil, "fgrep": nil, "rg": nil, "head": nil, "tail": nil,
	"wc": nil, "stat": nil, "file": nil, "diff": nil, "du": nil, "tree": nil,
}

// runningOptions are options whose value is a program the command runs.
var runningOptions = []string{"-exec", "-toolexec", "-vettool", "--pager", "-O", "--open-files-in-pager", "--ext-diff"}

// namesRunningOption reports whether a word among words is an option of
// runningOptions. The go command takes -exec and --exec alike.
func namesRunningOption(words []string) bool {
	return slices.ContainsFunc(words, func(w string) bool {
		name, _, _ := strings.Cut(strings.Trim(w, `"'`), "=")
		return slices.Contains(runningOptions, name) || slices.Contains(runningOptions, strings.TrimPrefix(name, "-"))
	})
}

// operandOnly reports whether the words before a word are a program that never
// runs its operands, with its subcommand, such as go test or git diff. An
// option that runs a program (-exec, --pager) anywhere before, or an option
// right before the word whose value it may be, keeps the word a candidate.
func operandOnly(before []string) bool {
	subs, ok := operandPrograms[ProgramName(strings.Trim(before[0], `"'`))]
	if !ok || namesRunningOption(before[1:]) {
		return false
	}
	if len(before) > 1 && strings.HasPrefix(before[len(before)-1], "-") {
		return false
	}
	return subs == nil || len(before) > 1 && slices.Contains(subs, before[1])
}

// testNameOptions are the options of go test whose value is a test name.
var testNameOptions = []string{"-run", "-skip"}

// goTestNameValue reports whether words[i] is the test name of go test -run
// or -skip, the word after the option or its value after =, which go test
// matches and never runs. A value with shell syntax, or a go test with an
// option that runs a program, is left to the deny check.
func goTestNameValue(words []string, i int) bool {
	if i < 2 || len(words) < 3 || ProgramName(words[0]) != "go" || words[1] != "test" || namesRunningOption(words) {
		return false
	}
	name, value := words[i-1], words[i]
	if option, attached, found := strings.Cut(words[i], "="); found && strings.HasPrefix(words[i], "-") {
		name, value = option, attached
	}
	return slices.Contains(testNameOptions, name) && !strings.ContainsAny(value, "$`()<>\\\"'")
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
		if agent && i > 0 && tail == words[i] && operandOnly(words[:i]) {
			continue // a plain operand of a program that never runs it, as in go test ./pkg
		}
		if agent && goTestNameValue(words, i) {
			continue
		}
		for _, rule := range denyRules(p, lead) {
			if rule.Action == "exec" && agentRule(rule) == agent && roleApplies(rule, a.Role) && denyMatchesCommand(rule.Match, tail) {
				return Decision{Kind: Deny, Reason: "denied command inside this command", Rule: rule.Action + " " + rule.Match, Mode: m}, true
			}
		}
	}
	return Decision{}, false
}

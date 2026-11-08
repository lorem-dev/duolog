package policy

import (
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// readProgram reports whether name is a program that reads the files it is
// given, whatever options it has.
func readProgram(name string) bool {
	return name == "git" || name == "egrep" || name == "fgrep" || slices.Contains(readCommands, name) || slices.Contains(checkedReadCommands, name)
}

// stateDecision is the deny of the state rule.
func stateDecision(m mode.Mode) *Decision {
	return &Decision{Kind: Deny, Reason: "duolog state is not readable by workers", Rule: RuleState, Mode: m}
}

// inputGlobDeny is the deny of an input redirect from a glob. The shell expands
// the file of a redirect, and bash reads the one name it finds, so a glob that
// can name state or is aimed at a denied name reads that file.
func inputGlobDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string) *Decision {
	denies := readDenyPatterns(p, lead, a)
	for _, target := range inputTargets(part) {
		if !strings.ContainsAny(target, "*?[") {
			continue
		}
		if statePattern(workTarget(a, target)) {
			return stateDecision(m)
		}
		if globAimedAtDeny(target, denies, false) {
			return &Decision{Kind: Deny, Reason: "input redirect: a shell glob can name a file that policy denies; name the file", Rule: "read command", Mode: m}
		}
	}
	return nil
}

// wrappedReadDeny holds the deny of a read for the forms that hide the command
// from the plain reading: an assignment or a wrapper in front of it, a path to
// the program, or an option the gate does not know. A glob word of such a
// command that can name state is denied wherever it stands, and a wrapped read
// has the paths it names judged as the plain command's are. It only denies.
func wrappedReadDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action) *Decision {
	if a.Shell == "powershell" || a.Shell == "windows" {
		return nil
	}
	for _, part := range rawExecParts(a.Target) {
		if d := inputGlobDeny(p, lead, m, a, part); d != nil {
			return d
		}
		words, _ := shellWords(commandAfterKeywords(part))
		args, _ := splitRedirects(words)
		start := execOperandStart(wordTexts(args))
		if start < 1 || start > len(args) || !readProgram(programName(args[start-1].text)) {
			continue
		}
		if slices.ContainsFunc(args[start:], func(word shellWord) bool {
			return word.hasGlob() && !strings.HasPrefix(word.text, "-") && statePattern(workTarget(a, word.text))
		}) {
			return stateDecision(m)
		}
		if start > 1 || args[0].text != programName(args[0].text) {
			if d := innerReadDeny(p, lead, m, a, args[start-1:]); d != nil {
				return d
			}
		}
	}
	return nil
}

// innerReadDeny judges the paths of a command that a wrapper starts, its
// program at words[0], as readCommandDecision judges a plain one.
func innerReadDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, words []shellWord) *Decision {
	plain := slices.Clone(words)
	plain[0].text = programName(words[0].text)
	line := wordsLine(plain)
	if !readOnlyCommand(line) {
		return nil
	}
	targets, _ := readCommandTargets(line)
	for _, target := range withTopPathspecs(a, commandWords(line), targets) {
		if d, _ := readTargetDecision(p, lead, m, a, a.Shell, target, commandWords(line)); d.Kind == Deny {
			return &d
		}
	}
	return nil
}

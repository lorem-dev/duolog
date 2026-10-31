package gate

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/pathutil"
)

// hiddenDeny looks inside one command for what a deny must still see. The gate
// reads command text and runs no shell, so a deny has to hold for a command that
// another program starts (uv run git push) and for a path that a program other
// than a named read command is given (sed -n p .env).
func hiddenDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string) (Decision, bool) {
	words := strings.Fields(part)
	denies := slices.Clip(lead.Deny)
	for _, rule := range p.Rules {
		if rule.Decision == Deny {
			denies = append(denies, rule)
		}
	}
	for i := 0; i < len(words); i++ {
		tail := strings.Join(words[i:], " ")
		for _, rule := range denies {
			if rule.Action == "exec" && roleApplies(rule, a.Role) && denyMatchesCommand(rule.Match, tail) {
				return Decision{Deny, "denied command inside this command", rule.Action + " " + rule.Match, m}, true
			}
		}
	}
	for _, word := range words[1:] {
		target := pathWord(word)
		if target == "" {
			continue
		}
		if a.Work != "" {
			target = RelativeTarget(a.Work, a.Cwd, expandHome(target))
		}
		target = cleanPath(target)
		for _, rule := range denies {
			if rule.Action != "read" && rule.Action != "write" {
				continue
			}
			if matches(rule, Action{Kind: rule.Action, Target: target, Role: a.Role, Shell: a.Shell}) {
				return Decision{Deny, "this command names a path that policy denies", rule.Action + " " + rule.Match, m}, true
			}
		}
	}
	return Decision{}, false
}

func roleApplies(rule config.Rule, role string) bool {
	return len(rule.Roles) == 0 || slices.Contains(rule.Roles, role)
}

// A deny fails closed on spelling: rm and RM are one program on a disk that
// ignores case, and the canonical form catches rm -fr and git -C x push.
func denyMatchesCommand(pattern, command string) bool {
	targets := []string{strings.ToLower(command), strings.ToLower(denyCommand(command)), strings.ToLower(denyPowerShell(command))}
	for _, alternative := range expandBraces(pattern) {
		alternative = strings.ToLower(alternative)
		for _, target := range targets {
			if starMatch(alternative, target) {
				return true
			}
		}
	}
	return false
}

// pathWord returns the part of a word that can name a file: the value of --x=y,
// and the file of curl's @file. A word that is only an option names none.
func pathWord(word string) string {
	word = strings.Trim(word, "<>&|;()")
	if strings.HasPrefix(word, "-") {
		_, value, found := strings.Cut(word, "=")
		if !found {
			return ""
		}
		word = value
	}
	return strings.TrimPrefix(word, "@")
}

func expandHome(target string) string {
	if !strings.HasPrefix(target, "~/") {
		return target
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return target
	}
	return filepath.Join(home, target[2:])
}

// needsReview names command lines whose effect the gate cannot read from the
// text, even in full mode: a directory change moves every later relative path, a
// shell glob expands onto files no rule saw, and git -c can run any program as an
// alias, pager or hook.
func needsReview(parts []string) bool {
	for _, part := range parts {
		words := strings.Fields(strings.ToLower(part))
		if len(words) == 0 {
			continue
		}
		if !readOnlyCommand(part) && slices.ContainsFunc(words[1:], func(word string) bool { return strings.ContainsAny(word, "*?[") }) {
			return true
		}
		switch words[0] {
		case "cd", "pushd", "chdir", "set-location", "push-location", "sl":
			if len(parts) > 1 {
				return true
			}
		case "git":
			for _, word := range words[1:] {
				if word == "-c" || word == "--config-env" || strings.HasPrefix(word, "--config-env=") || strings.HasPrefix(word, "--exec-path") {
					return true
				}
				if !strings.HasPrefix(word, "-") {
					break
				}
			}
		}
	}
	return false
}

var windowsDrive = regexp.MustCompile(`^[a-zA-Z]:`)

// A path on Windows can reach a file under another spelling: .env::$DATA is the
// content of .env, and a trailing dot or space is dropped. windowsTarget returns
// the spelling that rules compare, and false for a stream name, which has no use
// a rule could judge.
func windowsTarget(target string) (string, bool) {
	drive := windowsDrive.FindString(target)
	parts := strings.Split(strings.TrimPrefix(pathutil.Slashes(target), drive), "/")
	for i, part := range parts {
		// $env:NAME is a PowerShell variable, which the shell check already asks about.
		if strings.Contains(part, ":") && !strings.HasPrefix(part, "$") {
			return target, false
		}
		if part != "." && part != ".." {
			parts[i] = strings.TrimRight(part, ". ")
		}
	}
	return drive + strings.Join(parts, "/"), true
}

func usesWindowsNames(a Action) bool {
	return runtime.GOOS == "windows" || a.Shell == "windows" || a.Shell == "powershell" || pathutil.Windows(a.Target)
}

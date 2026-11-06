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
// than a named read command is given (sed -n p .env). The words of a for or
// select header become the loop variable, which any command of the line may
// read or write: they are judged by the state read rule, and by the write
// rules when lineWrites says some command of the line can write.
func hiddenDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string, lineWrites bool) (Decision, bool) {
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
				return Decision{Kind: Deny, Reason: "denied command inside this command", Rule: rule.Action + " " + rule.Match, Mode: m}, true
			}
		}
	}
	header := loopHeader(part)
	writes := canWrite(part) || header && lineWrites
	for _, word := range words[1:] {
		target := pathWord(word)
		if target == "" {
			continue
		}
		if a.Work != "" {
			target = RelativeTarget(a.Work, a.Cwd, expandHome(target))
		}
		target = cleanPath(target)
		if header && StateRead(target) {
			return Decision{Kind: Deny, Reason: "duolog state is not readable by workers", Rule: RuleState, Mode: m}, true
		}
		for _, rule := range denies {
			if rule.Action != "read" && rule.Action != "write" || rule.Action == "write" && !writes {
				continue
			}
			if matches(rule, Action{Kind: rule.Action, Target: target, Role: a.Role, Shell: a.Shell}) {
				return Decision{Kind: Deny, Reason: "this command names a path that policy denies", Rule: rule.Action + " " + rule.Match, Mode: m}, true
			}
		}
	}
	return Decision{}, false
}

// redirectDeny holds a write deny for every file a redirect in the part writes.
// A redirect writes whatever it follows, so this judges every part: a read
// command, a closing done, fi or esac, and a group alike.
func redirectDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string) (Decision, bool) {
	for _, target := range redirectTargets(part) {
		if a.Work != "" {
			target = RelativeTarget(a.Work, a.Cwd, expandHome(target))
		}
		write := Action{Kind: "write", Target: cleanPath(target), Role: a.Role, Shell: a.Shell}
		for _, rule := range lead.Deny {
			if rule.Action == "write" && matches(rule, write) {
				return Decision{Kind: Deny, Reason: "this command redirects into a path that lead settings deny", Rule: "lead: " + rule.Action + " " + rule.Match, Mode: m}, true
			}
		}
		for _, rule := range p.Rules {
			if rule.Decision == Deny && rule.Action == "write" && matches(rule, write) {
				return Decision{Kind: Deny, Reason: "this command redirects into a path that policy denies", Rule: rule.Action + " " + rule.Match, Mode: m}, true
			}
		}
	}
	return Decision{}, false
}

// inputDeny holds the read rules, the state read rule among them, for every
// file an input redirect in the part reads. Like redirectDeny it judges every
// part, so done < file is judged as cat < file is.
func inputDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string) (Decision, bool) {
	shell := a.Shell
	if shell == "powershell" {
		shell = "windows"
	}
	for _, target := range inputTargets(part) {
		if a.Work != "" {
			target = RelativeTarget(a.Work, a.Cwd, expandHome(target))
		}
		d := Evaluate(p, lead, m, Action{Kind: "read", Target: target, Role: a.Role, Shell: shell})
		if d.Kind == Deny {
			d.Reason = "input redirect: " + d.Reason
			return d, true
		}
	}
	return Decision{}, false
}

// inputTargets returns the files that input redirects in a part read: the word
// after <, N< or <>, attached or apart. A here-document or here-string (<<,
// <<-, <<<) names no file, nor does a redirect from another stream (<&3, <&-).
func inputTargets(part string) []string {
	var targets []string
	words := strings.Fields(part)
	for i := 0; i < len(words); i++ {
		word := words[i]
		for word != "" {
			at := strings.IndexByte(word, '<')
			if at < 0 {
				break
			}
			rest := word[at+1:]
			if strings.HasPrefix(rest, "<") {
				// The delimiter or the string may be the next word.
				if strings.TrimLeft(rest, "<-") == "" {
					i++
				}
				break
			}
			if strings.HasPrefix(rest, "(") {
				// Process substitution, which the parser already finds uncertain.
				break
			}
			duplicate := strings.HasPrefix(rest, "&")
			rest = strings.TrimPrefix(strings.TrimPrefix(rest, "&"), ">")
			if rest == "" {
				if i+1 >= len(words) {
					break
				}
				i++
				rest = words[i]
			}
			target, next := rest, ""
			if end := strings.IndexAny(rest, "<>"); end >= 0 {
				target, next = rest[:end], rest[end:]
			}
			target = strings.Trim(target, "'\"&|;()")
			if target != "" && (!duplicate || target != "-" && strings.Trim(target, "0123456789") != "") {
				targets = append(targets, target)
			}
			word = next
		}
	}
	return targets
}

// redirectTargets returns the files that output redirects in a part write: the
// word after >, >>, >|, &>, N> or <>, attached or apart. Redirects may follow
// each other inside one word, as in >out>.git/config, so each one names its
// own file up to the next >. A redirect onto another stream, as in 2>&1 or
// >&-, names no file.
func redirectTargets(part string) []string {
	var targets []string
	pending, duplicate := false, false
	for _, word := range strings.Fields(part) {
		for word != "" {
			if !pending {
				at := strings.IndexByte(word, '>')
				if at < 0 {
					break
				}
				rest := strings.TrimLeft(word[at:], ">|&")
				duplicate = strings.Contains(word[at:len(word)-len(rest)], "&")
				word = rest
				if word == "" {
					// The file is the next word.
					pending = true
					break
				}
			}
			pending = false
			target, next := word, ""
			if at := strings.IndexByte(word, '>'); at >= 0 {
				target, next = word[:at], word[at:]
			}
			target = strings.Trim(target, "'\"<>&|;()")
			if target != "" && (!duplicate || target != "-" && strings.Trim(target, "0123456789") != "") {
				targets = append(targets, target)
			}
			word = next
		}
	}
	return targets
}

// canWrite reports whether a command part can write a file it names. A write
// rule judges only such a part. echo prints its words, so it writes only when
// a redirect sends the output to a file. A for or select header lists words
// for the loop variable; hiddenDeny judges it with the rest of the line. A
// case header alone, and a done, fi or esac with nothing but redirects, write
// nothing themselves. Anything else, a case line whose pattern the gate could
// not find among them, is taken to write. A keyword in front of a command, as
// in do ls, does not change what the command does.
func canWrite(part string) bool {
	words := strings.Fields(commandAfterKeywords(part))
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "for", "select":
		return false
	case "case":
		return len(words) != 3 || words[2] != "in"
	case "done", "fi", "esac":
		return !onlyRedirects(words[1:])
	case "echo", "printf":
		return strings.Contains(part, ">")
	}
	return !readOnlyCommand(strings.Join(words, " "))
}

// loopHeader reports whether a part is a for or select header, whose words
// become the loop variable.
func loopHeader(part string) bool {
	word, _ := nextShellWord(commandAfterKeywords(part))
	return word == "for" || word == "select"
}

// lineCanWrite reports whether any command of a shell line, other than a loop
// header, can write. It reads both the parsed parts and the raw ones, so a
// wrapper the parser leaves out still counts.
func lineCanWrite(command string, parts []string) bool {
	return slices.ContainsFunc(append(rawExecParts(command), parts...), func(part string) bool {
		return !loopHeader(part) && canWrite(part)
	})
}

// onlyRedirects reports whether words hold nothing but redirects and the files
// they name.
func onlyRedirects(words []string) bool {
	for i := 0; i < len(words); i++ {
		word := strings.TrimLeft(words[i], "0123456789")
		if !strings.HasPrefix(word, "<") && !strings.HasPrefix(word, ">") && !strings.HasPrefix(word, "&>") {
			return false
		}
		if strings.Trim(word, "<>&|") == "" {
			i++ // The file is the next word.
		}
	}
	return true
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

// patternOptions lists, per program, the options whose value is a pattern or
// a format rather than a file, so ExecOutside does not judge it as a path.
// Any other program's option is not known to take a value, so the word after
// it is judged as an operand.
var patternOptions = map[string][]string{
	"git":   {"--grep", "--author", "--committer", "--format", "--pretty"},
	"rg":    {"--regexp", "--glob", "--iglob"},
	"grep":  {"--regexp"},
	"egrep": {"--regexp"},
	"fgrep": {"--regexp"},
}

// ExecOutside checks file operands, not executable names or stream devices.
func ExecOutside(a Action) bool {
	parts, uncertain := ShellParts(a.Target, a.Shell)
	if uncertain || len(parts) == 0 {
		// Uncertain syntax can omit wrapped commands; retain their operands, and
		// keep the parsed parts, whose operands have their quotes removed.
		parts = append(rawExecParts(a.Target), parts...)
	}
	for _, part := range parts {
		// A redirect can stand anywhere, even between an option and its value,
		// so its file is judged on its own and the redirect leaves the words
		// before any of them is taken as an option's value.
		for _, target := range append(redirectTargets(part), inputTargets(part)...) {
			if outsideOperand(a, target) {
				return true
			}
		}
		words := withoutRedirects(strings.Fields(part))
		start := execOperandStart(words)
		var valued []string
		if start > 0 && start <= len(words) {
			valued = patternOptions[programName(words[start-1])]
		}
		options := true
		for i := start; i < len(words); i++ {
			word := strings.Trim(words[i], "'\"")
			if !options {
				// Past --, every word is an operand, even one that looks like an
				// option, so a leading - is part of the path.
				if outsideOperand(a, strings.Trim(word, "<>&|;()")) {
					return true
				}
				continue
			}
			if word == "--" {
				options = false
				continue
			}
			name, _, attached := strings.Cut(word, "=")
			if slices.Contains(valued, name) {
				if !attached {
					i++
				}
				continue
			}
			if outsideOperand(a, pathWord(word)) {
				return true
			}
		}
	}
	return false
}

// outsideOperand reports whether a word that names a file names one outside
// the work root. A stream device is not a file operand.
func outsideOperand(a Action, target string) bool {
	if target == "" {
		return false
	}
	target = expandHome(target)
	clean := pathutil.Clean(target)
	if slices.Contains([]string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr"}, clean) || strings.HasPrefix(clean, "/dev/fd/") {
		return false
	}
	if a.Work != "" {
		target = RelativeTarget(a.Work, a.Cwd, target)
	}
	return outside(target)
}

// withoutRedirects drops the redirects of a part and the files they name, so
// what is left are the program's own words. redirectTargets and inputTargets
// read the files.
func withoutRedirects(words []string) []string {
	var kept []string
	for i := 0; i < len(words); i++ {
		redirect := strings.TrimLeft(strings.Trim(words[i], "'\""), "0123456789")
		if !strings.HasPrefix(redirect, ">") && !strings.HasPrefix(redirect, "<") && !strings.HasPrefix(redirect, "&>") {
			kept = append(kept, words[i])
			continue
		}
		operator := strings.TrimLeft(redirect, "<>&|")
		if strings.HasPrefix(redirect, "<<") {
			operator = strings.TrimLeft(redirect, "<-")
		}
		if operator == "" {
			i++ // The file, or the here-document's delimiter, is the next word.
		}
	}
	return kept
}

// rawExecParts retains wrapped commands while splitting shell command lists.
// Quoted and escaped separators are operands, not command boundaries.
func rawExecParts(command string) []string {
	var parts []string
	var quote byte
	start, redirect := 0, -1
	escaped := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '>' {
			redirect = i
		}
		// >| is a clobber redirect, not a pipe.
		if c != ';' && c != '|' && c != '\n' && !(c == '&' && i+1 < len(command) && command[i+1] == '&') || c == '|' && redirect == i-1 {
			continue
		}
		parts = append(parts, command[start:i])
		if (c == '&' || c == '|') && i+1 < len(command) && command[i+1] == c {
			i++
		}
		start = i + 1
	}
	return append(parts, command[start:])
}

var execAssignment = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*=`)

// execOperandStart skips assignments, wrappers and the executable they start.
// wrapperValueOptions lists, per wrapper, the options written apart from
// their value (sudo -u alice): the value is skipped with the option.
var wrapperValueOptions = map[string][]string{
	"sudo":    {"-u", "-g", "-h", "-p", "-C", "-U", "-r", "-t", "-D", "-T", "--user", "--group", "--host", "--prompt", "--close-from", "--other-user", "--role", "--type", "--chdir", "--command-timeout"},
	"doas":    {"-u", "-C"},
	"env":     {"-u", "-C", "-S", "--unset", "--chdir", "--split-string"},
	"nice":    {"-n", "--adjustment"},
	"ionice":  {"-c", "-n", "-p", "-P", "-u", "--class", "--classdata"},
	"timeout": {"-s", "-k", "--signal", "--kill-after"},
	"xargs":   {"-a", "-d", "-E", "-I", "-L", "-n", "-P", "-s", "--arg-file", "--delimiter", "--max-args", "--max-procs", "--max-lines", "--max-chars"},
}

func execOperandStart(words []string) int {
	for i := 0; i < len(words); {
		word := strings.Trim(words[i], "'\"")
		if execAssignment.MatchString(word) {
			i++
			continue
		}
		switch filepath.Base(word) {
		case "command", "env", "sudo", "xargs", "eval", "exec", "sh", "bash", "zsh", "pwsh", "powershell", "cmd",
			"time", "nice", "ionice", "timeout", "nohup", "stdbuf", "setsid", "busybox", "doas", "watch", "parallel":
			i++
			for i < len(words) && strings.HasPrefix(words[i], "-") {
				// An option such as sudo -u takes the next word as its value,
				// so that word is not the program.
				takesValue := slices.Contains(wrapperValueOptions[filepath.Base(word)], words[i])
				i++
				if takesValue && i < len(words) {
					i++
				}
			}
			if filepath.Base(word) == "timeout" && i < len(words) {
				i++ // timeout's duration precedes the executable.
			}
		default:
			return i + 1
		}
	}
	return len(words)
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

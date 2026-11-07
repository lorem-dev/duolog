package policy

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// hiddenDeny looks inside one command for what a deny must still see. The gate
// reads command text and runs no shell, so a deny has to hold for a command that
// another program starts (uv run git push, innerCommandDeny, for every rule
// but the agent rules, which commandDeny judges) and for a path that a program
// other than a named read command is given (sed -n p .env). The words of a for
// or select header become the loop variable, which any command of the line
// may read or write: they are judged by the state read rule, and by the write
// rules when lineWrites says some command of the line can write.
func hiddenDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string, lineWrites bool) (Decision, bool) {
	if d, ok := innerCommandDeny(p, lead, m, a, part, false); ok {
		return d, true
	}
	words := strings.Fields(part)
	denies := denyRules(p, lead)
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
		if d, ok := writeDeny(p, lead, m, a, target, "this command redirects into a path that"); ok {
			return d, true
		}
	}
	return Decision{}, false
}

// writeDeny holds the write denies of the lead and the policy for one file
// that a command writes. what starts the reason.
func writeDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, target, what string) (Decision, bool) {
	if a.Work != "" {
		target = RelativeTarget(a.Work, a.Cwd, expandHome(target))
	}
	write := Action{Kind: "write", Target: cleanPath(target), Role: a.Role, Shell: a.Shell}
	for _, rule := range lead.Deny {
		if rule.Action == "write" && matches(rule, write) {
			return Decision{Kind: Deny, Reason: what + " lead settings deny", Rule: "lead: " + rule.Action + " " + rule.Match, Mode: m}, true
		}
	}
	for _, rule := range p.Rules {
		if rule.Decision == Deny && rule.Action == "write" && matches(rule, write) {
			return Decision{Kind: Deny, Reason: what + " policy denies", Rule: rule.Action + " " + rule.Match, Mode: m}, true
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
	"git":   {"--grep", "--author", "--committer"},
	"rg":    {"--regexp", "--glob", "--iglob"},
	"grep":  {"--regexp"},
	"egrep": {"--regexp"},
	"fgrep": {"--regexp"},
}

// attachedPatternOptions lists, per program, the options whose value is a
// format only after =: git reads a bare --pretty as the default format and
// takes no next word, so the word after it is judged as an operand.
var attachedPatternOptions = map[string][]string{
	"git": {"--format", "--pretty"},
}

// skipsPatternValue returns how a pattern option at word moves the reading:
// skip is true for a pattern option, and next is true when its value is the
// next word.
func skipsPatternValue(program, word string) (skip, next bool) {
	name, _, attached := strings.Cut(word, "=")
	switch {
	case slices.Contains(patternOptions[program], name):
		return true, !attached
	case attached && slices.Contains(attachedPatternOptions[program], name):
		return true, false
	}
	return false, false
}

// ownOperandCheck lists the programs whose words operandsOutside leaves
// alone when partFileTargets read the same command. The sed and awk parsers
// judge every input file and option value of theirs, and what a script
// names, so a script such as /^$/d is not taken for an absolute path. tr
// reads only standard input; its operands are sets.
var ownOperandCheck = []string{"sed", "gsed", "awk", "gawk", "mawk", "nawk", "tr"}

// ExecOutside checks file operands, not executable names or stream devices.
// A part whose program runs nothing it is given is read by shell words, so a
// quoted argument is one word; any other part is read word by word, so a path
// inside a quoted command, as in sh -c 'cat ../x', still counts. A cd may
// fail or not, so a relative path is judged from the folder the command
// starts in and from each folder that a cd of the line can lead to.
func ExecOutside(a Action) bool {
	if execOutsideFrom(a) {
		return true
	}
	if a.Shell == "powershell" {
		return false
	}
	folders, ok := cdFolders(a)
	if !ok {
		return true
	}
	return slices.ContainsFunc(folders, func(folder string) bool {
		from := a
		from.Cwd = folder
		return execOutsideFrom(from)
	})
}

// execOutsideFrom is ExecOutside with relative paths judged from a.Cwd alone.
func execOutsideFrom(a Action) bool {
	// $'...' or a brace expansion can make any word name a file outside.
	if unexpandedLine(a) {
		return true
	}
	// A script or an option can name a file inside one word.
	if fileTargetsOutside(a) {
		return true
	}
	var own []string
	for _, part := range rawExecParts(a.Target) {
		own = append(own, partFileTargets(part).own...)
	}
	if a.Shell == "powershell" {
		parts, uncertain := ShellParts(a.Target, a.Shell)
		if uncertain || len(parts) == 0 {
			// Uncertain syntax can omit wrapped commands; retain their operands, and
			// keep the parsed parts, whose operands have their quotes removed.
			parts = append(rawExecParts(a.Target), parts...)
		}
		return slices.ContainsFunc(parts, func(part string) bool { return wordsOutside(a, part, own) })
	}
	for _, part := range rawExecParts(a.Target) {
		command := commandAfterKeywords(part)
		if handled, outside := verbatimOutside(a, command, own); handled {
			if outside {
				return true
			}
			// A word that ends in ) may instead end a subshell, as in shellParts.
			if command == part || !strings.Contains(part, ")") {
				continue
			}
		}
		if wordsOutside(a, part, own) {
			return true
		}
	}
	return false
}

// ownedCommand reports whether a command, its program at words[0], is one
// that the sed, awk and tr parsers read, as own lists them; its words are
// theirs to judge. A part whose words only look like one, as FOO='1 sed' cat x
// does when split at blanks, is not.
func ownedCommand(words, own []string) bool {
	return len(words) > 0 && slices.Contains(ownOperandCheck, programName(words[0])) && slices.Contains(own, ownCommand(words))
}

// unquoted spells a word as the shell passes it on when its quotes and
// backslashes only group literal text: '..'/x is ../x.
var unquoted = strings.NewReplacer("'", "", "\"", "", "\\", "")

// wordsOutside reads a part word by word, with each word's quotes trimmed and
// removed, and with redirects found even inside quotes. The operands of a
// command in own are left to its parser, and the pattern of grep or rg and
// the value of a pattern option are no path, but only for the part's own
// program: a wrapper such as xargs can add options after the words, as in
// printf -- '-e\nx\n' | xargs grep /x, where -e gives the pattern and /x
// becomes a file to read, so behind a wrapper every word counts as a path.
// The script that a shell such as sh or bash runs is one too. A quoted word
// that the blanks split, or one that starts with -, can make any later word
// look like an option's value or a pattern, so then every word counts as a
// path, and so it does when a word may expand to an option (expandsWords).
// The operands of a program behind a wrapper are never the worker's own in
// its Claude session temp folder.
func wordsOutside(a Action, part string, own []string) bool {
	if redirectsOutside(a, part) {
		return true
	}
	words := withoutRedirects(strings.Fields(part))
	if at := assignedSubstitution(words); at >= 0 {
		return commandWordsOutside(a, words, at+1, "", nil, nil)
	}
	start := execOperandStart(words)
	if start < 1 || start > len(words) {
		return commandWordsOutside(a, words, start, "", nil, nil)
	}
	command := words[start-1:]
	if wrapper := lastWrapper(words); wrapper != "" {
		script := slices.Contains([]string{"sh", "bash", "zsh"}, wrapper) && fieldOutside(a, unquoted.Replace(command[0]), false)
		return script || commandWordsOutside(a, words, start, "", nil, nil)
	}
	if ownedCommand(command, own) {
		return false
	}
	temp := func(i int, options bool) bool { return rawTempOperand(a, part, command, i, options) }
	if splitQuotes(command) {
		return commandWordsOutside(a, words, start, "", temp, nil)
	}
	patterns, named := searchWords(a, command)
	program, patterns := surePatterns(command, programName(command[0]), patterns)
	return named || commandWordsOutside(a, words, start, program, temp, patterns)
}

// commandWordsOutside is wordsOutside for the words of a part from start on.
// program is the program at start-1, or "" when its option values and
// patterns are not known; temp, when not nil, reports whether the word at an
// index of the command, the program at 0, names a path in the worker's own
// Claude session temp folder that counts as its own; patterns marks the words
// of the command that are a search pattern.
func commandWordsOutside(a Action, words []string, start int, program string, temp func(i int, options bool) bool, patterns map[int]bool) bool {
	options := true
	for i := start; i < len(words); i++ {
		word := strings.Trim(withoutEscapes(a, words[i]), "'\"")
		skip, next := skipsPatternValue(program, word)
		switch {
		case patterns[i-start+1]:
		case options && word == "--":
			options = false
		case options && skip:
			if next {
				i++
			}
		case program == "find" && slices.Contains(findExecdir, word):
			// The command runs in each match's folder, so fileTargetsOutside
			// judges its words from the search roots instead.
			i = execdirEnd(a, words, i)
		case (fieldOutside(a, word, options) || fieldOutside(a, unquoted.Replace(words[i]), options)) && (temp == nil || !temp(i-start+1, options)):
			return true
		}
	}
	return false
}

// execdirEnd returns the index of the last word of the command that find's
// -execdir or -okdir at words[i] runs, before its ; or +.
func execdirEnd(a Action, words []string, i int) int {
	for i+1 < len(words) && !slices.Contains([]string{";", "+"}, strings.Trim(withoutEscapes(a, words[i+1]), `'"`)) {
		i++
	}
	return i
}

// fieldOutside reports whether a word of the word-by-word reading names a
// file outside the work root. Past --, every word is an operand, even one
// that looks like an option, so a leading - is part of the path.
func fieldOutside(a Action, word string, options bool) bool {
	if options {
		return outsideOperand(a, pathWord(word))
	}
	return outsideOperand(a, strings.Trim(word, "<>&|;()"))
}

// outsideOperand reports whether a word that names a file names one outside
// the work root. A stream device is not a file operand.
func outsideOperand(a Action, target string) bool {
	if target == "" {
		return false
	}
	if strings.Contains(target, `\`) && posixShell(a) {
		// A POSIX shell passes a backslash on as part of the word, or drops it
		// as an escape; it never separates folders. The word is outside when
		// either reading of it is.
		return outsideOperand(a, strings.ReplaceAll(target, `\`, "")) || literalOutside(a, target)
	}
	target = expandHome(target)
	if strings.HasPrefix(target, "~") {
		// ~name, or ~ where the home folder is not known.
		return true
	}
	if streamDevice(target) {
		return false
	}
	if a.Work != "" {
		target = RelativeTarget(a.Work, a.Cwd, target)
	}
	return outside(target)
}

// streamDevice reports whether a path names a stream of the command itself,
// such as /dev/null or /dev/fd/3, rather than a file.
func streamDevice(target string) bool {
	clean := pathutil.Clean(target)
	return slices.Contains([]string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr"}, clean) || strings.HasPrefix(clean, "/dev/fd/")
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
		if c != ';' && c != '|' && c != '\n' && (c != '&' || i+1 >= len(command) || command[i+1] != '&') || c == '|' && redirect == i-1 {
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
	"sudo": {"-u", "-g", "-h", "-p", "-C", "-U", "-r", "-t", "-D", "-T", "--user", "--group", "--host", "--prompt", "--close-from", "--other-user", "--role", "--type", "--chdir", "--command-timeout"},
	"doas": {"-u", "-C"},
	// BSD env's -P names the folders to look the program up in.
	"env":     {"-u", "-C", "-S", "-P", "--unset", "--chdir", "--split-string"},
	"exec":    {"-a"},
	"nice":    {"-n", "--adjustment"},
	"ionice":  {"-c", "-n", "-p", "-P", "-u", "--class", "--classdata"},
	"timeout": {"-s", "-k", "--signal", "--kill-after"},
	"stdbuf":  {"-i", "-o", "-e", "--input", "--output", "--error"},
	"xargs":   {"-a", "-d", "-E", "-I", "-L", "-n", "-P", "-s", "--arg-file", "--delimiter", "--max-args", "--max-procs", "--max-lines", "--max-chars"},
	// A shell's -o and -O name an option, also when written +o and +O.
	"sh":         {"-o", "-O", "+o", "+O", "--rcfile", "--init-file"},
	"bash":       {"-o", "-O", "+o", "+O", "--rcfile", "--init-file"},
	"zsh":        {"-o", "+o"},
	"caffeinate": {"-t", "-w"},
}

// wrapperOption reports whether a word after a wrapper is one of its
// options: it starts with -, or with + for a shell, as in sh +o noglob.
func wrapperOption(wrapper, word string) bool {
	return strings.HasPrefix(word, "-") || strings.HasPrefix(word, "+") && slices.Contains([]string{"sh", "bash", "zsh"}, wrapper)
}

func execOperandStart(words []string) int {
	return walkWrappers(words, nil)
}

// wrapperValue says what the value of a wrapper's option names.
type wrapperValue int

const (
	// valueCode is a file that the wrapper runs as code: a shell's startup
	// file.
	valueCode wrapperValue = iota + 1
	// valueRead is a file or folder that the wrapper reads: the arguments of
	// xargs -a, the folders that env -P looks the program up in.
	valueRead
	// valueFolder is the folder that the program runs in: env -C.
	valueFolder
)

// wrapperFileOptions lists, per wrapper, the options whose value names a
// file or a folder, and what it names. sudo's own options stay unjudged:
// sudo always needs the person.
var wrapperFileOptions = map[string]map[string]wrapperValue{
	"sh":    {"--rcfile": valueCode, "--init-file": valueCode},
	"bash":  {"--rcfile": valueCode, "--init-file": valueCode},
	"env":   {"-C": valueFolder, "--chdir": valueFolder, "-P": valueRead},
	"xargs": {"-a": valueRead, "--arg-file": valueRead},
}

// walkWrappers is execOperandStart. It calls visit, when not nil, for each
// option of a wrapper that takes a value, with that value: after = for a
// long option, after the letter in a cluster of short ones, as in env -iC/x,
// or else the next word.
func walkWrappers(words []string, visit func(wrapper, option, value string)) int {
	for i := 0; i < len(words); {
		word := strings.Trim(words[i], "'\"")
		if execAssignment.MatchString(word) {
			i++
			continue
		}
		wrapper := filepath.Base(word)
		if !slices.Contains(wrapperNames, wrapper) {
			return i + 1
		}
		i = skipWrapperOptions(wrapper, words, i+1, visit)
		if wrapper == "timeout" && i < len(words) {
			i++ // timeout's duration precedes the executable.
		}
	}
	return len(words)
}

// wrapperNames are the programs that run the program after them, as
// walkWrappers reads them.
var wrapperNames = []string{"command", "builtin", "env", "sudo", "xargs", "eval", "exec", "sh", "bash", "zsh", "pwsh", "powershell", "cmd",
	"time", "nice", "ionice", "timeout", "nohup", "stdbuf", "setsid", "busybox", "doas", "watch", "parallel", "caffeinate"}

// skipWrapperOptions returns the index of the first word from i on that is
// not an option of wrapper or the value of one, calling visit as
// walkWrappers says.
func skipWrapperOptions(wrapper string, words []string, i int, visit func(wrapper, option, value string)) int {
	for i < len(words) && wrapperOption(wrapper, words[i]) {
		// An option such as sudo -u takes the next word as its value, so that
		// word is not the program.
		option, value, apart := wrapperOptionValue(wrapper, words[i])
		i++
		if apart && i < len(words) {
			value = words[i]
			i++
		}
		if option != "" && visit != nil {
			visit(wrapper, option, value)
		}
	}
	return i
}

// wrapperOptionValue reads one option word of a wrapper. option is the
// option that takes a value, or "" for none; value is its value in the same
// word, and apart means the value is the next word instead.
func wrapperOptionValue(wrapper, word string) (option, value string, apart bool) {
	options := wrapperValueOptions[wrapper]
	switch {
	case strings.HasPrefix(word, "--"):
		name, value, attached := strings.Cut(word, "=")
		if slices.Contains(options, name) {
			return name, value, !attached
		}
	case strings.HasPrefix(word, "-") && len(word) > 2:
		// A cluster such as -iC: the first letter that takes a value takes
		// the rest of the word, or the next word when nothing is left.
		for j := 1; j < len(word); j++ {
			if letter := "-" + word[j:j+1]; slices.Contains(options, letter) {
				return letter, word[j+1:], j+1 == len(word)
			}
		}
	case slices.Contains(options, word):
		return word, "", true
	}
	return "", "", false
}

// wrapperTargets lists the files and folders that the options of the
// wrappers in front of a program name, as bash --rcfile and xargs -a do, and
// the folders that env -C runs the program in.
func wrapperTargets(words []string) (t fileTargets, folders []string) {
	walkWrappers(words, func(wrapper, option, value string) {
		if value == "" {
			return
		}
		switch wrapperFileOptions[wrapper][option] {
		case valueCode:
			t.codeFile(value)
		case valueRead:
			t.reads = append(t.reads, value)
		case valueFolder:
			// The program may read and write there.
			t.paths = append(t.paths, value)
			folders = append(folders, value)
		}
	})
	return t, folders
}

// expandHome expands ~ and ~/x to the home folder. ~name, the home folder
// of another login, stays as written; outsideOperand counts it as outside.
func expandHome(target string) string {
	if target != "~" && !strings.HasPrefix(target, "~/") {
		return target
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return target
	}
	return filepath.Join(home, strings.TrimPrefix(target[1:], "/"))
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

// windowsShortName matches an NTFS 8.3 name, such as DUOLOG~1 for .duolog.
var windowsShortName = regexp.MustCompile(`^[^~.]{1,6}~[0-9]{1,6}(\.[^.]{1,3})?$`)

// shortNamePath reports whether a segment of a relative read or write target
// may be the 8.3 name of another file, which no rule on the long name sees.
// NTFS and FAT resolve such names on Windows and on any host that mounts them,
// such as WSL under /mnt/c, so the check runs on every host and splits at
// either slash. An absolute path is left to the outside rules: the person who
// answers it sees the spelling.
func shortNamePath(target string) bool {
	return !pathutil.IsAbs(target) && slices.ContainsFunc(strings.Split(pathutil.Slashes(target), "/"), windowsShortName.MatchString)
}

func usesWindowsNames(a Action) bool {
	return runtime.GOOS == "windows" || a.Shell == "windows" || a.Shell == "powershell" || pathutil.Windows(a.Target)
}

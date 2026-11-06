package gate

import (
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/pathutil"
)

// fileTargets are the files one command part reads or writes that its plain
// operands do not show, such as the file of a sed r or w command. The other
// checks split a command at blanks, so such a file was judged, at best, as a
// piece of one relative path.
type fileTargets struct {
	reads, writes []string
	// paths are files that the part reads or writes; the gate cannot tell
	// which, so they are judged both ways.
	paths []string
	// named are values that name a file only if the program uses them so,
	// as FOO=/etc/x does for a program that reads FOO. Only the outside
	// check judges them.
	named []string
	// globs are the words with an unquoted glob character, which the shell
	// expands; see globOutside. The quoted parts of such a word are judged as
	// if they were glob text too.
	globs []string
	// own are the commands of sed, awk and tr that the parsers read, as
	// ownCommand writes them; operandsOutside leaves only those alone.
	own []string
	// unknown means the part can reach a file or run a program that the gate
	// cannot name, such as the command of sed's e.
	unknown bool
}

// ownCommand writes a command from its program word on, with quotes and
// backslashes dropped and single blanks, so the parser's words and the
// coarse check's words of one command compare equal.
func ownCommand(words []string) string {
	text := strings.NewReplacer("'", "", `"`, "", `\`, "").Replace(strings.Join(words, " "))
	return strings.Join(strings.Fields(text), " ")
}

func (t *fileTargets) add(other fileTargets) {
	t.own = append(t.own, other.own...)
	t.reads = append(t.reads, other.reads...)
	t.writes = append(t.writes, other.writes...)
	t.paths = append(t.paths, other.paths...)
	t.named = append(t.named, other.named...)
	t.globs = append(t.globs, other.globs...)
	t.unknown = t.unknown || other.unknown
}

// union is add for two readings of one command, which name many files alike:
// each file is listed once.
func (t *fileTargets) union(other fileTargets) {
	t.add(other)
	for _, list := range []*[]string{&t.reads, &t.writes, &t.paths, &t.named, &t.globs} {
		var seen []string
		for _, name := range *list {
			if !slices.Contains(seen, name) {
				seen = append(seen, name)
			}
		}
		*list = seen
	}
}

// codeFile notes a file that a program reads code from, such as the script
// of sed -f. The gate never sees what a file holds; a stream such as - or
// /dev/stdin can feed code that no earlier write showed, so it is unknown.
func (t *fileTargets) codeFile(name string) {
	clean := path.Clean(pathutil.Slashes(name))
	if name == "-" || clean == "/dev/stdin" || strings.HasPrefix(clean, "/dev/fd/") {
		t.unknown = true
	}
	t.reads = append(t.reads, name)
}

// partFileTargets lists the file targets of one part of a shell line, as
// rawExecParts splits it. The program after keywords and wrappers counts, and
// so does a command line that a shell runs, as in sh -c 'sed ...'.
func partFileTargets(part string) fileTargets {
	return commandFileTargets(part, 0)
}

// maxScriptDepth limits how far sh -c inside sh -c is read.
const maxScriptDepth = 3

func commandFileTargets(part string, depth int) fileTargets {
	return wordsFileTargets(scriptWords(commandAfterKeywords(part)), depth)
}

// wordsFileTargets is commandFileTargets for a command split into words.
func wordsFileTargets(split []scriptWord, depth int) fileTargets {
	var t fileTargets
	words := make([]string, len(split))
	for i, word := range split {
		words[i] = word.text
	}
	start := execOperandStart(words)
	if start == 0 || start > len(words) {
		return t
	}
	if !slices.ContainsFunc(words, func(word string) bool { return !execAssignment.MatchString(word) }) {
		// Only assignments, which set variables that a later export hands on.
		t.add(assignmentTargets(words))
		return t
	}
	program, args := words[start-1], words[start:]
	t.add(assignmentTargets(words[:start-1]))
	for _, word := range split[start:] {
		if word.glob {
			t.globs = append(t.globs, word.text)
		}
	}
	if strings.ContainsAny(program, " \t\r\n") {
		// The wrapper runs a command line, as sh -c 'script' and eval 'script' do.
		if depth >= maxScriptDepth {
			t.unknown = true
			return t
		}
		for _, inner := range rawExecParts(program) {
			t.add(commandFileTargets(inner, depth+1))
		}
		return t
	}
	name := programName(program)
	if slices.Contains(ownOperandCheck, name) {
		t.own = append(t.own, ownCommand(words[start-1:]))
	}
	t.add(programTargets(name, args, split[start:], depth))
	return t
}

// programTargets lists the file targets that a program's own words name;
// split holds the same words as args.
func programTargets(name string, args []string, split []scriptWord, depth int) fileTargets {
	var t fileTargets
	switch name {
	case "export", "declare", "typeset", "readonly", "local":
		// They set a variable for the rest of the line.
		t.add(assignmentTargets(args))
	case "sed", "gsed":
		t.add(sedTargets(args))
	case "awk", "gawk", "mawk", "nawk":
		t.add(awkTargets(args))
	case "find":
		t.paths = attachedValues(name, args)
		t.add(findExecTargets(split, depth))
	default:
		t.paths = attachedValues(name, args)
	}
	return t
}

// findExecTargets judges the commands that find runs with -exec, -execdir,
// -ok and -okdir, each up to its ; or +.
func findExecTargets(words []scriptWord, depth int) fileTargets {
	var t fileTargets
	for i := 0; i < len(words); i++ {
		if !slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, words[i].text) {
			continue
		}
		if depth >= maxScriptDepth {
			t.unknown = true
			return t
		}
		end := i + 1
		for end < len(words) && words[end].text != ";" && words[end].text != "+" {
			end++
		}
		t.add(wordsFileTargets(words[i+1:end], depth+1))
		i = end
	}
	return t
}

// fileVariables name a file or a folder that a program reads: git's config
// and repository, the startup file of bash and sh, a library that the loader
// preloads, a search path for code. Any name that ends in _FILE or _CONFIG
// counts too.
var fileVariables = []string{
	"GIT_CONFIG", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_EXEC_PATH", "GIT_TEMPLATE_DIR", "GIT_SSH", "GIT_ASKPASS", "SSH_ASKPASS",
	"BASH_ENV", "ENV", "ZDOTDIR", "INPUTRC", "HOME", "XDG_CONFIG_HOME",
	"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH", "DYLD_FRAMEWORK_PATH",
	"PYTHONSTARTUP", "PYTHONPATH", "PYTHONHOME", "PERL5LIB", "RUBYLIB", "NODE_PATH",
	"KUBECONFIG", "DOCKER_CONFIG", "NETRC", "CURL_HOME", "GNUPGHOME",
}

// assignmentTargets judges the assignments in front of a program. The value
// of a file variable is read, and any value is named. A value is judged whole
// and split at : as PATH is; the whole keeps a Windows drive.
func assignmentTargets(words []string) fileTargets {
	var t fileTargets
	for _, word := range words {
		if !execAssignment.MatchString(word) {
			continue
		}
		name, value, _ := strings.Cut(word, "=")
		pieces := []string{value}
		if strings.Contains(value, ":") {
			pieces = append(pieces, strings.Split(value, ":")...)
		}
		t.named = append(t.named, pieces...)
		if slices.Contains(fileVariables, name) || strings.HasSuffix(name, "_FILE") || strings.HasSuffix(name, "_CONFIG") {
			t.reads = append(t.reads, pieces...)
		}
	}
	return t
}

// attachedTextShorts lists, per program, the short options whose value in the
// same word is text, such as the separator of cut -d/ or the pattern of
// grep -e/api/. Any other option's attached value that starts like a path is
// judged as one: -o/tmp/out, -C../peer, -t~/bin, curl's -d@file.
var attachedTextShorts = map[string]string{
	"grep": "e", "egrep": "e", "fgrep": "e", "rg": "egr", "git": "eSG",
	"cut": "d", "sort": "t", "paste": "d", "join": "t", "column": "s", "xargs": "dEI",
}

// attachedValues lists the values that a program's short options carry in
// their own word and that look like paths.
func attachedValues(program string, args []string) []string {
	var values []string
	for _, word := range args {
		if word == "--" {
			break
		}
		if value := attachedValue(program, word); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// attachedValue returns the text after the option letters of a single-dash
// word, after an optional @, when it looks like a path: it starts with /, ~,
// . or a Windows drive, or it holds a / or a \ anywhere.
func attachedValue(program, word string) string {
	if !strings.HasPrefix(word, "-") || strings.HasPrefix(word, "--") {
		return ""
	}
	end := 1
	for end < len(word) && isOptionLetter(word[end]) {
		end++
	}
	if end == 1 || strings.IndexByte(attachedTextShorts[program], word[end-1]) >= 0 {
		return ""
	}
	if end > 2 && attachedDrive(word[end:]) {
		// The last letter is a drive, as in -oC:/out or -oC:\out.
		end--
	}
	value := strings.TrimPrefix(word[end:], "@")
	if value == "" || strings.IndexByte("/~.", value[0]) < 0 && !pathutil.IsAbs(value) && !strings.ContainsAny(value, `/\`) {
		return ""
	}
	return value
}

// attachedDrive reports whether the text after an option's letters makes
// the last letter a drive: a colon and a slash or backslash, or on Windows
// a colon alone, as in -oD:out, which names a path on drive D.
func attachedDrive(rest string) bool {
	return strings.HasPrefix(rest, ":/") || strings.HasPrefix(rest, `:\`) || runtime.GOOS == "windows" && strings.HasPrefix(rest, ":")
}

func isOptionLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// fileTargetsOutside reports whether a command line reads or writes a file
// outside the work root through a script or an option, or reaches one the
// gate cannot name. The raw parts keep each quoted script whole.
func fileTargetsOutside(a Action) bool {
	budget := newGlobBudget()
	for _, part := range rawExecParts(a.Target) {
		targets := partFileTargets(part)
		if targets.unknown || slices.ContainsFunc(targets.globs, func(glob string) bool { return globOutside(a, glob, budget) }) {
			return true
		}
		for _, target := range slices.Concat(targets.reads, targets.writes, targets.paths, targets.named) {
			if outsideOperand(a, target) {
				return true
			}
		}
	}
	return false
}

// globOutside reports whether an unquoted glob word can expand onto a path
// outside the work root. A segment that starts with a dot and a glob
// character counts as .., since bash before 5.2 and dash match .?, .* and
// .[!x] to it, and a class such as [[:punct:]] can match a dot where Go's
// path.Match cannot. Any other glob is expanded from the working folder, with
// each bracket expression written as ?, and every match is judged, so a
// symlink that leads out counts. An expansion that spends the budget of the
// line counts as outside, so the person decides.
func globOutside(a Action, word string, budget *globBudget) bool {
	target := pathWord(word)
	if target == "" {
		return false
	}
	segments := strings.Split(pathutil.Slashes(target), "/")
	for i, segment := range segments {
		dotDot, _ := path.Match(segment, "..")
		if strings.HasPrefix(segment, ".") && (dotDot || len(segment) > 1 && strings.IndexByte("*?[", segment[1]) >= 0) {
			segments[i] = ".."
		}
	}
	if outsideOperand(a, strings.Join(segments, "/")) {
		return true
	}
	folder := a.Cwd
	if folder == "" {
		folder = a.Work
	}
	pattern := pathutil.Native(bracketsAsAny(expandHome(target)))
	if folder == "" && !filepath.IsAbs(pattern) {
		return false
	}
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(folder, pattern)
	}
	matches, ok := budget.glob(pattern)
	return !ok || slices.ContainsFunc(matches, func(match string) bool { return outsideOperand(a, match) })
}

// bracketsAsAny writes each bracket expression of a shell glob as ?, which
// matches any one character, and a [ that opens none as ? too. Go's
// filepath.Match knows no POSIX class such as [[:alpha:]], reads the shell's
// [!x] and [[!] by other rules, and refuses []x], so its own reading of a
// class could miss a name the shell matches; ? matches every such name.
func bracketsAsAny(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '[' {
			b.WriteByte(pattern[i])
			continue
		}
		b.WriteByte('?')
		if end := bracketEnd(pattern, i); end > 0 {
			i = end
		}
	}
	return b.String()
}

// bracketEnd returns the index of the ] that ends the bracket expression
// opened at start, as bash reads it, or 0 when it is not closed within its
// path segment.
func bracketEnd(pattern string, start int) int {
	i := start + 1
	if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
		i++
	}
	if i < len(pattern) && pattern[i] == ']' {
		i++
	}
	for ; i < len(pattern) && pattern[i] != '/'; i++ {
		switch {
		case pattern[i] == ']':
			return i
		case pattern[i] == '[' && i+1 < len(pattern) && strings.IndexByte(".:=", pattern[i+1]) >= 0:
			if end := strings.Index(pattern[i+2:], string(pattern[i+1])+"]"); end >= 0 {
				i += 2 + end + 1
			}
		}
	}
	return 0
}

// reasonAsk is the ask of a read command whose paths the gate cannot list,
// such as a glob's. It needs the person when the line can leave the work
// root, as cat .?/x can.
func reasonAsk(a Action, m mode.Mode, reason string) *Decision {
	rule := "read command"
	if ExecOutside(a) {
		rule = RuleOutside
	}
	return &Decision{Kind: Ask, Reason: reason, Rule: rule, Mode: m}
}

// rawPartDeny holds the denies for the files that one raw part redirects
// into, redirects from, and names inside a script or an option.
func rawPartDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string) (Decision, bool) {
	if d, ok := redirectDeny(p, lead, m, a, part); ok {
		return d, true
	}
	if d, ok := inputDeny(p, lead, m, a, part); ok {
		return d, true
	}
	return fileTargetDeny(p, lead, m, a, part)
}

// fileTargetDeny holds the read rules, the state read rule among them, for
// every file that partFileTargets finds a part reading, and the write denies
// for every file it finds the part writing. A path that may be either is
// judged as a write when the part can write, and as a read.
func fileTargetDeny(p config.Policy, lead LeadRules, m mode.Mode, a Action, part string) (Decision, bool) {
	shell := a.Shell
	if shell == "powershell" {
		shell = "windows"
	}
	targets := partFileTargets(part)
	if canWrite(part) {
		targets.writes = append(targets.writes, targets.paths...)
	}
	for _, target := range targets.writes {
		if d, ok := writeDeny(p, lead, m, a, target, "this command writes a path that"); ok {
			return d, true
		}
	}
	for _, target := range append(targets.reads, targets.paths...) {
		if a.Work != "" {
			target = RelativeTarget(a.Work, a.Cwd, expandHome(target))
		}
		d := Evaluate(p, lead, m, Action{Kind: "read", Target: target, Role: a.Role, Shell: shell})
		if d.Kind == Deny {
			d.Reason = "a file this command reads: " + d.Reason
			return d, true
		}
	}
	return Decision{}, false
}

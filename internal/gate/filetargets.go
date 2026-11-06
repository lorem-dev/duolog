package gate

import (
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
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
	// unknown means the part can reach a file or run a program that the gate
	// cannot name, such as the command of sed's e.
	unknown bool
}

func (t *fileTargets) add(other fileTargets) {
	t.reads = append(t.reads, other.reads...)
	t.writes = append(t.writes, other.writes...)
	t.paths = append(t.paths, other.paths...)
	t.unknown = t.unknown || other.unknown
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
	var t fileTargets
	words := scriptWords(commandAfterKeywords(part))
	start := execOperandStart(words)
	if start == 0 || start > len(words) {
		return t
	}
	program, args := words[start-1], words[start:]
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
	switch name {
	case "sed", "gsed":
		t.add(sedTargets(args))
	default:
		t.paths = attachedValues(name, args)
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
	"awk": "Fv", "gawk": "Fv", "mawk": "Fv", "nawk": "Fv",
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
// word when it starts with /, ~, . or a Windows drive, after an optional @.
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
	if end > 2 && strings.HasPrefix(word[end:], ":/") {
		// The last letter is a drive, as in -oC:/out.
		end--
	}
	value := strings.TrimPrefix(word[end:], "@")
	if value == "" || strings.IndexByte("/~.", value[0]) < 0 && !pathutil.IsAbs(value) {
		return ""
	}
	return value
}

func isOptionLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// fileTargetsOutside reports whether a command line reads or writes a file
// outside the work root through a script or an option, or reaches one the
// gate cannot name. The raw parts keep each quoted script whole.
func fileTargetsOutside(a Action) bool {
	for _, part := range rawExecParts(a.Target) {
		targets := partFileTargets(part)
		if targets.unknown {
			return true
		}
		for _, target := range slices.Concat(targets.reads, targets.writes, targets.paths) {
			if outsideOperand(a, target) {
				return true
			}
		}
	}
	return false
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

// scriptWords splits a part into the words the shell passes on: quotes group
// text and go away, and a backslash keeps the next character. It expands
// nothing. A redirect and the file it names are left out, since the redirect
// checks judge that file.
func scriptWords(part string) []string {
	var words []string
	drop := false
	for _, word := range splitScriptWords(part) {
		switch {
		case drop:
			drop = false
		case word.redirect:
			// The file is the next word when the operator stands alone.
			drop = strings.Trim(word.text, "0123456789<>&|-") == ""
		default:
			words = append(words, word.text)
		}
	}
	return words
}

// scriptWord is one word of scriptWords. redirect means it starts with an
// unquoted redirect operator.
type scriptWord struct {
	text     string
	redirect bool
}

func splitScriptWords(part string) []scriptWord {
	var words []scriptWord
	var current scriptWord
	var b strings.Builder
	var quote byte
	inWord, escaped := false, false
	for i := 0; i < len(part); i++ {
		c := part[i]
		switch {
		case escaped:
			b.WriteByte(c)
			escaped = false
		case c == '\\' && quote != '\'':
			escaped, inWord = true, true
		case quote != 0:
			if c != quote {
				b.WriteByte(c)
			} else {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case strings.IndexByte(" \t\r\n", c) >= 0:
			if inWord {
				current.text = b.String()
				words = append(words, current)
				current, inWord = scriptWord{}, false
				b.Reset()
			}
		default:
			if !inWord {
				current.redirect = redirectAt(part[i:])
			}
			b.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		current.text = b.String()
		words = append(words, current)
	}
	return words
}

// redirectAt reports whether text starts with a redirect operator, after an
// optional stream number.
func redirectAt(text string) bool {
	operator := strings.TrimLeft(text, "0123456789")
	return strings.HasPrefix(operator, "<") || strings.HasPrefix(operator, ">") || strings.HasPrefix(operator, "&>")
}

// sedTargets reads the options and the script of sed, GNU or BSD. The input
// files are read, and written too with -i or -I. A script file given by -f is
// read; what it holds stays unseen, as a Makefile's does. Where the two seds
// read a word differently, the reading that names more files wins, and a word
// that neither reads as known makes the command unknown.
func sedTargets(args []string) fileTargets {
	o := sedOptions{args: args}
	for o.i = 0; o.i < len(args); o.i++ {
		word := args[o.i]
		switch {
		case word == "--":
			o.operands = append(o.operands, args[o.i+1:]...)
			o.i = len(args)
		case word == "-" || !strings.HasPrefix(word, "-"):
			o.operands = append(o.operands, word)
		case strings.HasPrefix(word, "--"):
			o.long(word)
		default:
			o.short(word[1:])
		}
	}
	t := o.targets
	operands := o.operands
	scripts := o.scripts
	if !o.given {
		if len(operands) == 0 {
			t.unknown = true
			return t
		}
		scripts, operands = operands[:1], operands[1:]
	}
	sedScript(strings.Join(scripts, "\n"), &t)
	t.reads = append(t.reads, operands...)
	if o.inPlace {
		t.writes = append(t.writes, operands...)
	}
	return t
}

// sedOptions holds what sed's options say while sedTargets reads them.
type sedOptions struct {
	args              []string
	i                 int
	scripts, operands []string
	targets           fileTargets
	// given means -e or -f gave the script, so every operand is a file.
	given, inPlace bool
}

// next returns the word after the option, its value.
func (o *sedOptions) next() string {
	if o.i+1 < len(o.args) {
		o.i++
		return o.args[o.i]
	}
	o.targets.unknown = true
	return ""
}

// script notes the value of -e or -f.
func (o *sedOptions) script(file bool, value string) {
	if file {
		o.targets.reads = append(o.targets.reads, value)
	} else {
		o.scripts = append(o.scripts, value)
	}
	o.given = true
}

func (o *sedOptions) long(word string) {
	name, value, attached := strings.Cut(word, "=")
	switch name {
	case "--expression", "--file":
		if !attached {
			value = o.next()
		}
		o.script(name == "--file", value)
	case "--in-place":
		o.inPlace = true
	case "--line-length":
	case "--quiet", "--silent", "--debug", "--posix", "--sandbox", "--regexp-extended", "--separate", "--unbuffered",
		"--null-data", "--zero-terminated", "--follow-symlinks", "--binary", "--help", "--version":
		o.targets.unknown = o.targets.unknown || attached
	default:
		o.targets.unknown = true
	}
}

func (o *sedOptions) short(cluster string) {
	for j := 0; j < len(cluster); j++ {
		letter, rest := cluster[j], cluster[j+1:]
		switch {
		case letter == 'e' || letter == 'f':
			if rest == "" {
				rest = o.next()
			}
			o.script(letter == 'f', rest)
			return
		case letter == 'i' || letter == 'I' || letter == 'l':
			// GNU -i takes its suffix in the same word, BSD -i and -I the next
			// one, and GNU -l a number where BSD -l takes none. The next word
			// stays a script or a file, which names more.
			o.inPlace = o.inPlace || letter != 'l'
			return
		case !strings.ContainsRune("nsuzErab", rune(letter)):
			o.targets.unknown = true
		}
	}
}

// sedScript adds the files that a sed script reads and writes. r and R read
// the file named up to the end of the line, w, W and the s///w flag write it,
// with or without blanks before the name. e and the s///e flag run a command.
// A script the parser cannot read makes the targets unknown.
func sedScript(script string, t *fileTargets) {
	p := sedParser{s: script, t: t}
	for more := true; more; {
		more = p.command()
	}
}

// sedParser reads one sed script; i is its position in s.
type sedParser struct {
	s string
	i int
	t *fileTargets
}

const digits = "0123456789"

func (p *sedParser) more() bool { return p.i < len(p.s) }

func (p *sedParser) skip(set string) {
	for p.more() && strings.IndexByte(set, p.s[p.i]) >= 0 {
		p.i++
	}
}

// line moves to the end of the line and returns the text it passed.
func (p *sedParser) line() string {
	end := strings.IndexByte(p.s[p.i:], '\n')
	if end < 0 {
		end = len(p.s) - p.i
	}
	text := p.s[p.i : p.i+end]
	p.i += end
	return text
}

// file returns the file name of r, R, w, W or s///w.
func (p *sedParser) file() string {
	p.skip(" \t")
	name := p.line()
	if name == "" {
		p.t.unknown = true
	}
	return name
}

// fail makes the targets unknown and stops the parser.
func (p *sedParser) fail() bool {
	p.t.unknown = true
	p.i = len(p.s)
	return false
}

// delimited moves past text that ends at an unescaped delim.
func (p *sedParser) delimited(delim byte) bool {
	for p.more() {
		c := p.s[p.i]
		p.i++
		switch c {
		case '\\':
			p.i++
		case delim:
			return true
		}
	}
	return false
}

// address moves past one address, if there is one: a line number, first~step,
// $, /regex/ or \cregexc, with the I and M flags.
func (p *sedParser) address() bool {
	switch {
	case !p.more():
	case strings.IndexByte(digits, p.s[p.i]) >= 0:
		p.skip(digits)
		if p.more() && p.s[p.i] == '~' {
			p.i++
			p.skip(digits)
		}
	case p.s[p.i] == '$':
		p.i++
	case p.s[p.i] == '/' || p.s[p.i] == '\\':
		if p.s[p.i] == '\\' {
			p.i++
		}
		if !p.more() {
			return false
		}
		delim := p.s[p.i]
		p.i++
		if !p.delimited(delim) {
			return false
		}
		p.skip("IM")
	}
	return true
}

// addresses moves past the addresses of a command and a !.
func (p *sedParser) addresses() bool {
	if !p.address() {
		return false
	}
	p.skip(" \t")
	if p.more() && p.s[p.i] == ',' {
		p.i++
		p.skip(" \t")
		if p.more() && (p.s[p.i] == '+' || p.s[p.i] == '~') {
			p.i++
			p.skip(digits)
		} else if !p.address() {
			return false
		}
	}
	p.skip("! \t")
	return p.more()
}

// command reads one command and reports whether another may follow.
func (p *sedParser) command() bool {
	p.skip(" \t\n;")
	if !p.more() {
		return false
	}
	if p.s[p.i] == '#' {
		p.line()
		return true
	}
	if !p.addresses() {
		return p.fail()
	}
	command := p.s[p.i]
	p.i++
	if command == '{' {
		return true
	}
	if !p.body(command) {
		return p.fail()
	}
	p.skip(" \t")
	if p.more() && strings.IndexByte(";\n}#", p.s[p.i]) < 0 {
		return p.fail()
	}
	return true
}

// body reads what follows a command letter.
func (p *sedParser) body(command byte) bool {
	switch command {
	case '}', '=', 'd', 'D', 'g', 'G', 'h', 'H', 'n', 'N', 'p', 'P', 'x', 'z', 'F':
	case 'l', 'L', 'q', 'Q':
		p.skip(" \t")
		p.skip(digits)
	case 'a', 'i', 'c':
		// The text runs to the end of the line, and on while a line ends in a
		// backslash.
		for strings.HasSuffix(p.line(), "\\") && p.more() {
			p.i++
		}
	case ':', 'b', 't', 'T', 'v':
		for p.more() && p.s[p.i] != '\n' && p.s[p.i] != ';' {
			p.i++
		}
	case 'r', 'R':
		p.t.reads = append(p.t.reads, p.file())
	case 'w', 'W':
		p.t.writes = append(p.t.writes, p.file())
	case 'e':
		p.t.unknown = true
		p.line()
	case 's', 'y':
		return p.substitute(command)
	default:
		return false
	}
	return true
}

// substitute reads s/regex/replacement/flags or y/source/dest/.
func (p *sedParser) substitute(command byte) bool {
	if !p.more() || p.s[p.i] == '\n' || p.s[p.i] == '\\' {
		return false
	}
	delim := p.s[p.i]
	p.i++
	if !p.delimited(delim) {
		return false
	}
	if !p.delimited(delim) {
		return false
	}
	for command == 's' && p.more() {
		switch flag := p.s[p.i]; {
		case strings.IndexByte("gpiImM"+digits, flag) >= 0:
			p.i++
		case flag == 'e':
			p.t.unknown = true
			p.i++
		case flag == 'w':
			p.i++
			p.t.writes = append(p.t.writes, p.file())
			return true
		default:
			return true
		}
	}
	return true
}

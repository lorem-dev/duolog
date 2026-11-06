package gate

import "strings"

// sedTargets reads the options and the script of sed, GNU or BSD. The input
// files are read, and written too with -i or -I. A script file given by -f is
// read; what it holds stays unseen, as a Makefile's does. Each reading reads
// the script by the rules of its own sed, so a BSD bracket expression may
// hold the delimiter. GNU sed takes the backup suffix of -i only in the same
// word, and BSD sed takes the next word when the option ends its word. Then
// both readings are judged, and the files of both count. A reading whose
// script does not parse is one that its sed would refuse, so it adds
// nothing, unless the other does not parse either. Where both seds read the
// words alike, either may run the script, so it must parse by both rules.
func sedTargets(args []string) fileTargets {
	gnu, gnuFailed, branched := sedReading(args, false)
	bsd, bsdFailed, _ := sedReading(args, true)
	switch {
	case !branched:
	case gnuFailed && !bsdFailed:
		return bsd
	case bsdFailed && !gnuFailed:
		return gnu
	}
	gnu.union(bsd)
	return gnu
}

// sedReading reads sed's words as GNU sed does, or with bsd as BSD sed does.
// failed means the script does not parse, and branched that an -i or -I
// ends its word, where the two seds differ.
func sedReading(args []string, bsd bool) (t fileTargets, failed, branched bool) {
	o := sedOptions{args: args, bsd: bsd}
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
	t = o.targets
	operands := o.operands
	scripts := o.scripts
	if !o.given {
		if len(operands) == 0 {
			t.unknown = true
			return t, true, o.branched
		}
		scripts, operands = operands[:1], operands[1:]
	}
	failed = sedScript(strings.Join(scripts, "\n"), &t, bsd)
	// Both seds refuse to edit standard input in place, so such a reading,
	// as BSD's of sed -i s/a/b/ x, fails.
	failed = failed || o.inPlace && len(operands) == 0
	t.reads = append(t.reads, operands...)
	if o.inPlace {
		t.writes = append(t.writes, operands...)
		for _, suffix := range o.suffixes {
			for _, operand := range operands {
				t.writes = append(t.writes, operand+suffix)
			}
		}
	}
	return t, failed, o.branched
}

// sedOptions holds what sed's options say while sedReading reads them.
type sedOptions struct {
	args              []string
	i                 int
	scripts, operands []string
	targets           fileTargets
	// given means -e or -f gave the script, so every operand is a file.
	given, inPlace bool
	// suffixes are the backup suffixes of -i, -I and --in-place.
	suffixes []string
	// bsd reads the word after an -i or -I that ends its word as the
	// suffix, and branched notes such an option.
	bsd, branched bool
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
		o.targets.codeFile(value)
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
		o.backup(value)
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
		case letter == 'i' || letter == 'I':
			// GNU -i takes its suffix in the same word, BSD -i and -I the next
			// one.
			o.inPlace = true
			if rest == "" && o.i+1 < len(o.args) {
				o.branched = true
				if o.bsd {
					o.i++
					rest = o.args[o.i]
				}
			}
			o.backup(rest)
			return
		case letter == 'l':
			// GNU -l takes a number where BSD -l takes none. The next word stays
			// a script or a file, which names more.
			return
		case !strings.ContainsRune("nsuzErab", rune(letter)):
			o.targets.unknown = true
		}
	}
}

// backup notes the backup suffix of an in-place edit, which writes each
// input with the suffix added. GNU sed puts the input's name for each * and
// reads a / as a folder, so a suffix with either is unknown.
func (o *sedOptions) backup(suffix string) {
	switch {
	case suffix == "":
	case strings.ContainsAny(suffix, "/*"):
		o.targets.unknown = true
	default:
		o.suffixes = append(o.suffixes, suffix)
	}
}

// sedScript adds the files that a sed script reads and writes. r and R read
// the file named up to the end of the line, w, W and the s///w flag write it,
// with or without blanks before the name. e and the s///e flag run a command.
// With bsd it reads the script as BSD sed does, whose regular expressions
// end a bracket expression at its own ]. A script the parser cannot read
// makes the targets unknown, and sedScript reports it.
func sedScript(script string, t *fileTargets, bsd bool) (failed bool) {
	p := sedParser{s: script, t: t, bsd: bsd}
	for more := true; more; {
		more = p.command()
	}
	return p.failed
}

// sedParser reads one sed script; i is its position in s, and failed means
// the script does not parse. bsd reads it by BSD sed's rules.
type sedParser struct {
	s           string
	i           int
	t           *fileTargets
	failed, bsd bool
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
	p.t.unknown, p.failed = true, true
	p.i = len(p.s)
	return false
}

// delimited moves past text that ends at an unescaped delim. In a regular
// expression read by BSD rules, a bracket expression may hold the delimiter.
func (p *sedParser) delimited(delim byte, regex bool) bool {
	for p.more() {
		c := p.s[p.i]
		if c == '[' && regex && p.bsd && delim != '[' {
			if !p.bracket() {
				return false
			}
			continue
		}
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

// bracket moves past a bracket expression as BSD sed's compile_ccl does: a
// ] right after [ or [^ is a member, [:class:], [.coll.] and [=equiv=] hold
// a ] of their own, and the first other ] ends it. A backslash is a member.
func (p *sedParser) bracket() bool {
	p.i++
	if p.more() && p.s[p.i] == '^' {
		p.i++
	}
	if p.more() && p.s[p.i] == ']' {
		p.i++
	}
	for p.more() && p.s[p.i] != ']' {
		if p.s[p.i] == '[' && p.i+1 < len(p.s) && strings.IndexByte(".:=", p.s[p.i+1]) >= 0 {
			end := strings.Index(p.s[p.i+2:], string(p.s[p.i+1])+"]")
			if end < 0 {
				return false
			}
			p.i += 2 + end + 2
			continue
		}
		p.i++
	}
	if !p.more() {
		return false
	}
	p.i++
	return true
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
		if !p.delimited(delim, true) {
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
	if !p.delimited(delim, true) {
		return false
	}
	// BSD sed reads both strings of y with the regular expression's rule.
	if !p.delimited(delim, command == 'y') {
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

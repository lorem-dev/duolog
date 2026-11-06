package gate

import "strings"

// awkTargets reads the options and the program of awk: POSIX awk, gawk, mawk
// or BSD awk. -f, -E and --exec read the program from a file, and -i and
// --include read an include file; all of them are reads, as are the input
// files. -o, -p and -d with a file in the same word write it, and -D reads
// it. -l and --load load a shared library, which runs code the gate cannot
// see, as does an option it does not know.
func awkTargets(args []string) fileTargets {
	o := awkOptions{args: args}
	for ; o.i < len(args); o.i++ {
		word := args[o.i]
		if word == "--" {
			o.i++
			break
		}
		if word == "-" || !strings.HasPrefix(word, "-") {
			break
		}
		switch {
		case word == "-safe" || word == "-version":
			// BSD awk spells these with one dash.
		case strings.HasPrefix(word, "--"):
			o.long(word)
		default:
			o.short(word[1:])
		}
	}
	t := o.targets
	operands := args[min(o.i, len(args)):]
	programs := o.programs
	if !o.given {
		if len(operands) == 0 {
			t.unknown = true
			return t
		}
		programs, operands = append(programs, operands[0]), operands[1:]
	}
	for _, program := range programs {
		awkProgram(program, &t)
	}
	for _, operand := range operands {
		// An operand such as n=1 assigns a variable instead of naming a file.
		if operand != "-" && !execAssignment.MatchString(operand) {
			t.reads = append(t.reads, operand)
		}
	}
	return t
}

// awkOptions holds what awk's options say while awkTargets reads them.
type awkOptions struct {
	args     []string
	i        int
	programs []string
	targets  fileTargets
	// given means a file or --source gave the program, so every operand is
	// an input file.
	given bool
}

// value returns the value of the option at i: the text in the same word, or
// else the next word.
func (o *awkOptions) value(attached string, inWord bool) string {
	if inWord {
		return attached
	}
	if o.i+1 < len(o.args) {
		o.i++
		return o.args[o.i]
	}
	o.targets.unknown = true
	return ""
}

func (o *awkOptions) long(word string) {
	name, value, attached := strings.Cut(word, "=")
	switch name {
	case "--file", "--exec":
		o.targets.codeFile(o.value(value, attached))
		o.given = true
	case "--include":
		o.targets.codeFile(o.value(value, attached))
	case "--source":
		o.programs = append(o.programs, o.value(value, attached))
		o.given = true
	case "--assign", "--field-separator":
		o.value(value, attached)
	case "--pretty-print", "--profile", "--dump-variables":
		if attached {
			o.targets.writes = append(o.targets.writes, value)
		}
	case "--debug":
		if attached {
			o.targets.reads = append(o.targets.reads, value)
		}
	case "--characters-as-bytes", "--traditional", "--copyright", "--gen-pot", "--help", "--lint", "--lint-old", "--bignum",
		"--use-lc-numeric", "--non-decimal-data", "--optimize", "--no-optimize", "--posix", "--re-interval", "--sandbox",
		"--version", "--csv", "--compat":
	default:
		// --load and any option the gate does not know.
		o.targets.unknown = true
	}
}

func (o *awkOptions) short(cluster string) {
	for j := 0; j < len(cluster); j++ {
		letter, rest := cluster[j], cluster[j+1:]
		switch {
		case strings.IndexByte("fEi", letter) >= 0:
			o.targets.codeFile(o.value(rest, rest != ""))
			o.given = o.given || letter != 'i'
			return
		case letter == 'e':
			o.programs = append(o.programs, o.value(rest, rest != ""))
			o.given = true
			return
		case letter == 'v' || letter == 'F':
			o.value(rest, rest != "")
			return
		case strings.IndexByte("opd", letter) >= 0:
			o.optionalFile(rest, true)
			return
		case letter == 'D':
			o.optionalFile(rest, false)
			return
		case strings.IndexByte("bcCghkLMnNOPrsStV", letter) < 0:
			// -l loads a library, mawk's -W takes words the gate does not
			// read, and any other letter is unknown.
			o.targets.unknown = true
			return
		}
	}
}

// optionalFile notes the file of an option whose file is optional and, when
// given, stands in the same word.
func (o *awkOptions) optionalFile(file string, write bool) {
	switch {
	case file == "":
	case write:
		o.targets.writes = append(o.targets.writes, file)
	default:
		o.targets.reads = append(o.targets.reads, file)
	}
}

// awkProgram adds the files that an awk program reads and writes: the file
// after getline <, the file after > or >> in a print or printf statement, and
// the file of gawk's @include. A pipe, |&, system() and @load run code, and a
// file that is not one string literal cannot be named; each of them makes the
// targets unknown. A < or > elsewhere compares.
func awkProgram(program string, t *fileTargets) {
	p := awkParser{s: program, t: t, print: -1}
	for p.i < len(p.s) {
		p.step()
	}
}

// awkParser reads one awk program; i is its position in s.
type awkParser struct {
	s     string
	i     int
	t     *fileTargets
	depth int
	// print is the parenthesis depth of the open print statement, or -1.
	print int
	// getline means a getline waits for its < file.
	getline bool
	// operand means the last token ends an operand, so a / divides instead
	// of starting a regular expression.
	operand bool
}

func (p *awkParser) step() {
	c := p.s[p.i]
	switch {
	case c == '"':
		p.str()
		p.operand = true
	case c == '/' && !p.operand:
		p.regex()
		p.operand = true
	case c == '#':
		p.comment()
	case c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z':
		p.word()
	case c == '@':
		p.i++
		p.directive()
	case strings.IndexByte("<>|", c) >= 0:
		p.redirect(c)
	default:
		if !p.increment(c) {
			p.punctuation(c)
		}
	}
}

// increment moves past ++ or --. After an operand it ends the operand, as
// in n++ / 2, and before one it leaves the operand to come, so operand stays
// as it was.
func (p *awkParser) increment(c byte) bool {
	if c != '+' && c != '-' || p.i+1 >= len(p.s) || p.s[p.i+1] != c {
		return false
	}
	p.i += 2
	return true
}

// punctuation moves past one character that is not a token of its own.
func (p *awkParser) punctuation(c byte) {
	p.i++
	switch {
	case c == '(':
		p.depth++
		p.operand = false
	case c == ')' || c == ']' || c == '.' || '0' <= c && c <= '9':
		if c == ')' {
			p.depth--
		}
		p.operand = true
	case strings.IndexByte(";{}\n", c) >= 0:
		// A statement ends.
		p.print, p.getline, p.operand = -1, false, false
	case c != ' ' && c != '\t' && c != '\\' && c != '\r':
		p.operand = false
	}
}

// str moves past a string literal and returns its text.
func (p *awkParser) str() string {
	start := p.i + 1
	for p.i++; p.i < len(p.s) && p.s[p.i] != '"'; p.i++ {
		if p.s[p.i] == '\\' {
			p.i++
		}
	}
	text := p.s[start:min(p.i, len(p.s))]
	p.i++
	return text
}

// regex moves past a regular expression; a bracket expression may hold a /.
func (p *awkParser) regex() {
	for p.i++; p.i < len(p.s) && p.s[p.i] != '/' && p.s[p.i] != '\n'; p.i++ {
		switch p.s[p.i] {
		case '\\':
			p.i++
		case '[':
			if end := strings.IndexByte(p.s[p.i+1:], ']'); end >= 0 {
				p.i += end + 1
			}
		}
	}
	p.i++
}

func (p *awkParser) comment() {
	if end := strings.IndexByte(p.s[p.i:], '\n'); end >= 0 {
		p.i += end
		return
	}
	p.i = len(p.s)
}

func (p *awkParser) name() string {
	start := p.i
	for p.i < len(p.s) && (p.s[p.i] == '_' || isOptionLetter(p.s[p.i])) {
		p.i++
	}
	return p.s[start:p.i]
}

// word reads a name; print, printf, getline, system and ARGV matter. The
// main loop and a bare getline read the files in ARGV, which a program can
// fill by assignment, split, sub or getline var, so any use of ARGV counts.
// ARGC alone cannot name a file.
func (p *awkParser) word() {
	p.operand = false
	switch p.name() {
	case "print", "printf":
		p.print = p.depth
	case "getline":
		p.getline = true
	case "system":
		p.t.unknown = true
	case "ARGV":
		p.t.unknown = true
		p.operand = true
	case "return", "case", "in":
	default:
		p.operand = true
	}
}

// directive reads gawk's @include, @load and @namespace.
func (p *awkParser) directive() {
	switch p.name() {
	case "include":
		if file, ok := p.file(); ok {
			p.t.reads = append(p.t.reads, file)
		}
	case "namespace":
	default:
		p.t.unknown = true
	}
}

// redirect reads <, > or | and the file after a redirect.
func (p *awkParser) redirect(c byte) {
	p.i++
	next := byte(0)
	if p.i < len(p.s) {
		next = p.s[p.i]
	}
	p.operand = false
	switch {
	case c == '|' && next == '|', c != '|' && next == '=':
		// ||, <= and >=.
		p.i++
	case c == '|':
		// A pipe to or from a command, or a coprocess.
		p.t.unknown = true
	case c == '>' && p.print >= 0 && p.depth == p.print:
		if next == '>' {
			p.i++
		}
		if file, ok := p.file(); ok {
			p.t.writes = append(p.t.writes, file)
		}
	case c == '<' && p.getline:
		p.getline = false
		if file, ok := p.file(); ok {
			p.t.reads = append(p.t.reads, file)
		}
	}
}

// file reads the file after a redirect or @include. Only one string literal
// names a file the gate can judge.
func (p *awkParser) file() (string, bool) {
	p.skipBlanks()
	if p.i >= len(p.s) || p.s[p.i] != '"' {
		p.t.unknown = true
		return "", false
	}
	file := p.str()
	p.skipBlanks()
	if p.i < len(p.s) && strings.IndexByte(";})\n,", p.s[p.i]) < 0 {
		// The name goes on, as in "out" $1.
		p.t.unknown = true
	}
	p.operand = true
	return file, true
}

func (p *awkParser) skipBlanks() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

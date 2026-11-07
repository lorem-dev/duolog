package policy

import (
	"slices"
	"strings"
)

// dataPrograms never run a word they are given. Their arguments, and the
// here-documents and here-strings they read, are data, so text in them that
// names a program starts nothing.
var dataPrograms = []string{"cat", "echo", "printf", "tee", "true", ":", "mkdir", "touch", "wc", "head", "tail", "ls"}

// dataMark stands for a quoted string in the masked line of shellData. It is
// one character that separates nothing, so the word it is in stays one word,
// and it names no program.
const dataMark = "\x01"

// heredoc is a here-document whose body the next line starts.
type heredoc struct {
	delimiter string
	// tabs is <<-, which strips leading tabs from each line.
	tabs bool
	// quoted is a quoted delimiter, which keeps the body from expansion.
	quoted bool
}

// shellData reads a POSIX command line for the text that only data programs
// receive: quoted strings and here-document bodies. masked is the line with
// each quoted string as dataMark and each body left out. ok is false, and the
// caller keeps reading the whole line, when the line holds anything the
// reading cannot place: an unquoted backslash, $'...', a comment, a
// substitution, a subshell, a group or a brace, an unclosed quote or body, a
// substitution, a backslash or ${ in a double-quoted string, a substitution
// or a backslash in an unquoted body, a program named by a variable or a
// glob, an assignment of quoted text, or quoted text or a body that reaches a
// program not in dataPrograms, also through a pipe that a line end continues.
func shellData(command string) (masked string, ok bool) {
	d := dataScanner{command: command}
	for d.i < len(command) {
		if !d.step() {
			return "", false
		}
	}
	if len(d.pending) > 0 || !dataOnly(d.masked.String()) {
		return "", false
	}
	return d.masked.String(), true
}

// commandParts are the commands of an exec action as the agent rules and the
// search for a denied command inside another command read them: the parts of
// the masked line when shellData reads it, so that quoted text and bodies
// that only data programs receive start nothing, or else parts, the parts of
// the line as written. Every other rule judges parts.
func commandParts(a Action, parts []string) []string {
	if a.Kind != "exec" || a.Shell == "powershell" {
		return parts
	}
	masked, ok := shellData(a.Target)
	if !ok {
		return parts
	}
	maskedParts, _ := readShellParts(masked, a.Shell)
	return maskedParts
}

// dataScanner is the state of shellData: the line, the place in it, the
// masked line it writes and the here-documents whose bodies the next line
// starts.
type dataScanner struct {
	command string
	i       int
	masked  strings.Builder
	pending []heredoc
}

// step reads one character, quoted string, here-document operator or line
// end, and reports false when the line cannot be read this way.
func (d *dataScanner) step() bool {
	c, rest := d.command[d.i], d.command[d.i:]
	switch {
	case c == '\\', c == '$' && strings.HasPrefix(rest, "$'"), c == '$' && strings.HasPrefix(rest, `$"`):
		return false
	case c == '#' && (d.i == 0 || strings.IndexByte(" \t\n;&|()", d.command[d.i-1]) >= 0):
		// A comment can hide a quote, which would pair with one after it.
		return false
	case c == '\'' || c == '"':
		return d.quote(c)
	case strings.HasPrefix(rest, "<<<"):
		d.copy(3, "<<<")
	case strings.HasPrefix(rest, "<<"):
		h, end, found := heredocStart(d.command, d.i)
		if !found {
			return false
		}
		d.pending = append(d.pending, h)
		d.copy(end-d.i, "<<"+dataMark)
	case c == '\n' && len(d.pending) > 0:
		d.copy(1, "\n")
		return d.bodies()
	default:
		d.copy(1, string(c))
	}
	return true
}

// copy moves past n characters, which the masked line writes as mask.
func (d *dataScanner) copy(n int, mask string) {
	d.masked.WriteString(mask)
	d.i += n
}

// quote masks the string that the quote q at the place starts.
func (d *dataScanner) quote(q byte) bool {
	end, closed := d.i+1+strings.IndexByte(d.command[d.i+1:], q), true
	if q == '"' {
		end, closed = doubleQuoteEnd(d.command, d.i)
	}
	if !closed || end <= d.i {
		return false
	}
	d.copy(end+1-d.i, dataMark)
	return true
}

// bodies skips the bodies of the pending here-documents, which start at the
// place, with their closing lines.
func (d *dataScanner) bodies() bool {
	for _, h := range d.pending {
		next, found := heredocBody(d.command, d.i, h)
		if !found {
			return false
		}
		d.i = next
	}
	d.pending = nil
	return true
}

// doubleQuoteEnd returns the index of the " that closes the double-quoted
// string at command[start]. closed is false when none does, or when the
// string holds a substitution, which runs a command, a backslash, which can
// join $ and ( across a line end, or ${, whose word may hold quotes of its
// own.
func doubleQuoteEnd(command string, start int) (end int, closed bool) {
	for j := start + 1; j < len(command); j++ {
		switch c := command[j]; {
		case c == '"':
			return j, true
		case c == '`' || c == '\\', c == '$' && j+1 < len(command) && (command[j+1] == '(' || command[j+1] == '{'):
			return 0, false
		}
	}
	return 0, false
}

// heredocStart reads the here-document operator at command[i], << or <<-,
// and its delimiter word. end is the index after the word.
func heredocStart(command string, i int) (h heredoc, end int, found bool) {
	j := i + 2
	if j < len(command) && command[j] == '-' {
		h.tabs = true
		j++
	}
	for j < len(command) && (command[j] == ' ' || command[j] == '\t') {
		j++
	}
	var word strings.Builder
	for j < len(command) && strings.IndexByte(" \t\n;|&<>()", command[j]) < 0 {
		c := command[j]
		if strings.IndexByte("\\$`", c) >= 0 {
			return heredoc{}, 0, false
		}
		if c != '\'' && c != '"' {
			word.WriteByte(c)
			j++
			continue
		}
		closing := strings.IndexByte(command[j+1:], c)
		if closing < 0 || c == '"' && strings.ContainsAny(command[j+1:j+1+closing], "\\$`") {
			return heredoc{}, 0, false
		}
		word.WriteString(command[j+1 : j+1+closing])
		h.quoted = true
		j += closing + 2
	}
	h.delimiter = word.String()
	return h, j, h.delimiter != ""
}

// heredocBody skips the body of h, which starts at command[i], and its
// closing line. found is false when no line closes it, or when an unquoted
// body holds a substitution or a backslash: a backslash and a line end join
// two lines, which can make $ and ( one substitution, or hide the closing
// line from the shell.
func heredocBody(command string, i int, h heredoc) (next int, found bool) {
	for i < len(command) {
		line, after := command[i:], len(command)
		if end := strings.IndexByte(line, '\n'); end >= 0 {
			line, after = line[:end], i+end+1
		}
		closing := line
		if h.tabs {
			closing = strings.TrimLeft(line, "\t")
		}
		if closing == h.delimiter {
			return after, true
		}
		if !h.quoted && (strings.ContainsAny(line, "`\\") || strings.Contains(line, "$(")) {
			return 0, false
		}
		i = after
	}
	return 0, false
}

// dataOnly reports whether every simple command of a masked line that
// receives data, quoted text or a here-document, is a data program, and so is
// every command of a pipeline that holds one.
func dataOnly(masked string) bool {
	if strings.ContainsAny(masked, "`(){}\\") {
		return false
	}
	carries, inert := false, true // of the current pipeline
	start := 0
	for i := 0; i <= len(masked); i++ {
		width, pipe := separatorAt(masked, i)
		if width == 0 {
			continue
		}
		data, program, ok := dataCommand(masked[start:i])
		if !ok {
			return false
		}
		carries, inert = carries || data, inert && program
		if !pipe {
			if carries && !inert {
				return false
			}
			carries, inert = false, true
		}
		i += width - 1
		start = i + 1
	}
	return true
}

// separatorAt returns the width of the command separator at masked[i], 0 when
// there is none, and whether it is a pipe. The end of the line separates too.
// 2>&1, &> f and >| f are redirects, and a line end after | or |& continues
// the pipeline.
func separatorAt(masked string, i int) (width int, pipe bool) {
	if i == len(masked) {
		return 1, false
	}
	c, two, prev := masked[i], masked[i:min(i+2, len(masked))], byte(0)
	if i > 0 {
		prev = masked[i-1]
	}
	if strings.IndexByte(";\n&|", c) < 0 || c == '&' && (prev == '>' || prev == '<' || two == "&>") || c == '|' && prev == '>' ||
		c == '\n' && pipeOpen(masked[:i]) {
		return 0, false
	}
	switch two {
	case "|&":
		return 2, true
	case "&&", "||":
		return 2, false
	}
	return 1, c == '|'
}

// pipeOpen reports whether a masked line, up to a line end, ends in a pipe,
// | or |&, which the next command continues.
func pipeOpen(before string) bool {
	before = strings.TrimRight(before, " \t\n")
	return strings.HasSuffix(before, "|&") || strings.HasSuffix(before, "|") && !strings.HasSuffix(before, "||")
}

// dataCommand reads one simple command of a masked line. data says that it
// receives quoted text or a here-document, and program that it is a data
// program. ok is false when its program is named by a variable, a glob, a
// redirect or quoted text, when an assignment holds quoted text, which a later
// command could run as $X, or when it receives data and is no data program.
func dataCommand(text string) (data, program, ok bool) {
	data = strings.Contains(text, dataMark) || strings.Contains(text, "<<")
	words := strings.Fields(text)
	for len(words) > 0 && execAssignment.MatchString(words[0]) {
		if strings.Contains(words[0], dataMark) {
			return false, false, false
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return data, true, !data
	}
	name := words[0]
	if strings.ContainsAny(name, "$*?[]~<>="+dataMark) {
		return false, false, false
	}
	// printf -v assigns its output to a variable, which a later $X could run.
	program = slices.Contains(dataPrograms, name) &&
		(name != "printf" || !slices.ContainsFunc(words[1:], func(w string) bool { return strings.HasPrefix(w, "-v") }))
	return data, program, !data || program
}

package policy

import (
	"regexp"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// goRoot is the one substitution the gate reads: it names the Go toolchain's
// own folder, the way around a version manager's shim. A program from any
// other substitution stays unknown.
const goRoot = "$(go env GOROOT)"

// goRootPrograms are the programs of the toolchain's bin folder.
var goRootPrograms = []string{"go", "gofmt"}

// goRootSettings change which toolchain go env GOROOT names, or, for IFS, how
// the shell splits the folder it prints into words.
var goRootSettings = regexp.MustCompile(`GOROOT|GOENV|GOTOOLCHAIN|IFS`)

// inertPrograms never run what a here-document gives them. cat needs its
// output in /dev/null, so the body is not passed on either.
var inertPrograms = []string{"cat", ":", "true"}

// protectedOrPlain is protectedDecision, and then plainDecision.
func protectedOrPlain(p config.Policy, lead LeadRules, m mode.Mode, a Action) (Decision, bool) {
	if d, ok := protectedDecision(a, m); ok {
		return d, true
	}
	return plainDecision(p, lead, m, a)
}

// plainDecision judges a POSIX exec line that plainExec reads: as written
// first, so that a deny of it stands, and otherwise as the plain line. ok is
// false for any other action, which Evaluate goes on to judge itself.
func plainDecision(p config.Policy, lead LeadRules, m mode.Mode, a Action) (Decision, bool) {
	if a.asWritten || a.Kind != "exec" || a.Shell == "powershell" || a.Shell == "windows" {
		return Decision{}, false
	}
	plain, ok := plainExec(a.Target)
	if !ok {
		return Decision{}, false
	}
	a.asWritten = true
	if d := Evaluate(p, lead, m, a); d.Kind == Deny {
		return d, true
	}
	a.Target = plain
	return Evaluate(p, lead, m, a), true
}

// plainExec returns a POSIX command line with the forms that the rules can
// judge as plain commands: $(go env GOROOT)/bin/go and gofmt, quoted or not,
// as the first word of a command become go and gofmt; and a whole line that
// gives a here-document only to cat >/dev/null, : or true is left out with its
// body. A variable that holds the toolchain's path is not read: a quoted name,
// as in export "G""O=git", can set it again past any check of the name. ok is
// false when nothing changed, or when the line holds anything else the reading
// cannot place: another substitution, a backtick, $'...' or $"...", ${...}, a
// comment, an unclosed quote or body, a GOROOT, GOENV, GOTOOLCHAIN or IFS, or a
// function, alias or other redefiners word that can make go or cat run
// something else. The caller judges the line as written first, so a deny of it
// stands.
func plainExec(command string) (string, bool) {
	s := plainScanner{command: command, lineStart: true, commandStart: true}
	for s.i < len(command) {
		if !s.step() {
			return "", false
		}
	}
	out := s.out.String()
	if s.endsInert {
		// The line end before a here-document that ended the line would leave
		// an empty last command.
		out = strings.TrimRight(out, " \t\n")
	}
	switch {
	case s.quote != 0 || len(s.pending) > 0 || !s.changed || strings.TrimSpace(out) == "":
		return "", false
	case s.toolchain && goRootSettings.MatchString(out):
		return "", false
	case redefines(out):
		return "", false
	}
	return out, true
}

// plainScanner is the state of plainExec: the place in the line, the line it
// writes, the quote it is in, the here-documents whose bodies the next line
// starts, and whether the place starts a line or a command.
type plainScanner struct {
	command                 string
	i                       int
	out                     strings.Builder
	quote                   byte
	pending                 []heredoc
	lineStart, commandStart bool
	// last is the last character written outside quotes and bodies that is
	// not a blank: a | or & there continues the command on the next line, and
	// a > or < there makes the | or & after it part of a redirect.
	last               byte
	changed, toolchain bool
	// inert is a here-document left out, and endsInert one that ends the line.
	inert, endsInert bool
}

// redefiners can make go, cat, : or true run something else, or run a command
// before each one, so a line with them is read as written.
var redefiners = []string{"alias", "unalias", "function", "enable", "hash", "trap"}

// redefines reports whether a line defines a function or holds a word of
// redefiners.
func redefines(line string) bool {
	words := strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune(" \t\n;&|(){}'\"", r) })
	return strings.Contains(strings.ReplaceAll(line, " ", ""), "()") || slices.ContainsFunc(words, func(w string) bool { return slices.Contains(redefiners, w) })
}

func (s *plainScanner) write(text string) {
	s.out.WriteString(text)
	s.i += len(text)
}

// step reads one character, quoted run, operator, program or inert
// here-document, and reports false when the line cannot be read this way.
func (s *plainScanner) step() bool {
	rest := s.command[s.i:]
	c := rest[0]
	switch {
	case s.quote == '\'':
		if c == '\'' {
			s.quote = 0
		}
		s.write(rest[:1])
		return true
	case s.quote == '"':
		return s.doubleQuoted()
	case c == ' ' || c == '\t':
		s.write(rest[:1])
		return true
	case s.lineStart && s.inertHeredoc():
		return true
	case s.commandStart && s.program():
		return true
	}
	s.lineStart, s.commandStart = false, false
	if !s.unquoted() {
		return false
	}
	if c != '\n' {
		s.last = c
	}
	return true
}

// doubleQuoted reads one character inside double quotes, where a substitution
// or ${ cannot be read.
func (s *plainScanner) doubleQuoted() bool {
	rest := s.command[s.i:]
	switch c := rest[0]; {
	case c == '\\' && len(rest) > 1:
		s.write(rest[:2])
	case c == '`', c == '$' && len(rest) > 1 && (rest[1] == '(' || rest[1] == '{'):
		return false
	default:
		if c == '"' {
			s.quote = 0
		}
		s.write(rest[:1])
	}
	return true
}

// unquoted reads one character or operator outside quotes.
func (s *plainScanner) unquoted() bool {
	rest := s.command[s.i:]
	c := rest[0]
	switch {
	case c == '\\':
		s.write(rest[:min(2, len(rest))])
	case c == '`', c == '#', c == '$' && len(rest) > 1 && strings.IndexByte("('\"{", rest[1]) >= 0:
		return false
	case c == '\'' || c == '"':
		s.quote = c
		s.write(rest[:1])
	case strings.HasPrefix(rest, "<<"):
		return s.heredocHeader()
	case c == '\n':
		return s.lineEnd()
	case c == ';' || c == '|' || c == '&':
		// In >|, >& and <& the word after is a file, not a program.
		s.commandStart = c == ';' || s.last != '>' && s.last != '<'
		s.write(rest[:1])
	default:
		s.write(rest[:1])
	}
	return true
}

// heredocHeader writes the << at the place, a here-string as it is and a
// here-document's operator and delimiter, whose body the next line starts.
func (s *plainScanner) heredocHeader() bool {
	if strings.HasPrefix(s.command[s.i:], "<<<") {
		s.write("<<<")
		return true
	}
	h, end, found := heredocStart(s.command, s.i)
	if !found {
		return false
	}
	s.pending = append(s.pending, h)
	s.write(s.command[s.i:end])
	return true
}

// lineEnd writes the line end at the place and the bodies of the pending
// here-documents after it. The next line starts a line unless a | or & before
// continues the command.
func (s *plainScanner) lineEnd() bool {
	s.write("\n")
	for _, h := range s.pending {
		next, found := heredocBody(s.command, s.i, h)
		if !found {
			return false
		}
		s.write(s.command[s.i:next])
	}
	s.pending = nil
	s.lineStart = s.last != '|' && s.last != '&'
	s.commandStart = true
	return true
}

// toolchainWord reads the toolchain program at the start of text, quoted or
// not, and returns it and the length of its word. The word ends at a blank,
// a command separator or the end of the line.
func toolchainWord(text string) (program string, n int) {
	for _, name := range goRootPrograms {
		for _, word := range []string{goRoot + "/bin/" + name, `"` + goRoot + "/bin/" + name + `"`, `"` + goRoot + `"/bin/` + name} {
			if strings.HasPrefix(text, word) && wordEnds(text[len(word):]) {
				return name, len(word)
			}
		}
	}
	return "", 0
}

// wordEnds reports whether a shell word ends where rest starts.
func wordEnds(rest string) bool {
	return rest == "" || strings.IndexByte(" \t\n;&|", rest[0]) >= 0
}

// program writes the toolchain program that starts the command at the place.
func (s *plainScanner) program() bool {
	name, n := toolchainWord(s.command[s.i:])
	if n == 0 {
		return false
	}
	s.out.WriteString(name)
	s.i += n
	s.changed, s.toolchain, s.commandStart, s.lineStart, s.last = true, true, false, false, 'o'
	return true
}

// inertHeredoc leaves out the line at the place when it only gives a
// here-document to an inertPrograms program, with the body and its closing
// line. An unquoted body may hold no $, backtick or backslash, since the shell
// would expand them.
func (s *plainScanner) inertHeredoc() bool {
	rest := s.command[s.i:]
	header, _, _ := strings.Cut(rest, "\n")
	at := strings.Index(header, "<<")
	if at < 0 || strings.HasPrefix(header[at:], "<<<") || strings.Contains(header[at+2:], "<<") {
		return false
	}
	h, end, found := heredocStart(header, at)
	if !found {
		return false
	}
	words := strings.Fields(header[:at] + " " + header[end:])
	if len(words) == 0 || !slices.Contains(inertPrograms, words[0]) {
		return false
	}
	switch strings.Join(words[1:], " ") {
	case ">/dev/null", "> /dev/null", "1>/dev/null", "1> /dev/null":
	case "":
		if words[0] == "cat" {
			return false
		}
	default:
		return false
	}
	if len(header) == len(rest) {
		return false
	}
	bodyStart := s.i + len(header) + 1
	next, found := heredocBody(s.command, bodyStart, h)
	if !found {
		return false
	}
	if !h.quoted && strings.ContainsAny(s.command[bodyStart:next], "$`\\") {
		return false
	}
	s.i = next
	s.changed, s.inert = true, true
	s.endsInert = strings.TrimSpace(s.command[next:]) == ""
	return true
}

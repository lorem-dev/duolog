package policy

import (
	"regexp"
	"slices"
	"strings"
)

// Frames of the shell context that shellExpansion tracks.
const (
	frameSingle = '\''
	frameDouble = '"'
	frameParen  = '('
	frameTick   = '`'
	frameParam  = 'p'
	frameBrace  = 'b'
)

// shortNameTilde matches a ~ inside a name, before a digit: the RUNNER~1 of a
// Windows home folder. No shell expands it, and the gate reads only a leading ~
// as the home folder.
var shortNameTilde = regexp.MustCompile(`[A-Za-z0-9_]~[0-9]`)

// quotedRunUnsafe reports whether a quoted run holds a character of unsafe,
// apart from the tilde of a short name.
func quotedRunUnsafe(run, unsafe string) bool {
	return strings.ContainsAny(shortNameTilde.ReplaceAllString(run, ""), unsafe)
}

// unexpandedLine reports whether an exec action's line holds an expansion
// that shellExpansion finds. PowerShell and cmd have neither.
func unexpandedLine(a Action) bool {
	return a.Kind == "exec" && a.Shell != "powershell" && a.Shell != "windows" && shellExpansion(a.Target)
}

// expansionBuildsPath reports whether a POSIX exec line builds a path from an
// expansion: a word after the program that starts with an expansion and holds
// a /, as $HOME/.ssh/x does, or any word with $IFS, which splits one word into
// several, as cat$IFS/x and cat x$IFS.env do. The gate cannot know where such a
// word leads. A variable whose values the line shows is left out (shownNames),
// since each value is judged where the line sets it. echo and printf only
// print their words, and the body of a here-document is no command.
func expansionBuildsPath(a Action) bool {
	if a.Kind != "exec" || a.Shell == "powershell" || a.Shell == "windows" {
		return false
	}
	line := withoutHeredocBodies(a.Target)
	shown := shownNames(line)
	return slices.ContainsFunc(rawExecParts(line), func(part string) bool {
		part = commandAfterKeywords(part)
		first, _ := nextShellWord(part)
		return !slices.Contains([]string{"echo", "printf"}, programName(first)) && partBuildsPath(part, shown)
	})
}

var (
	loopHeaderWords = regexp.MustCompile(`(?:^|[\s;&|(])for\s+([A-Za-z_][A-Za-z0-9_]*)\s+in\s+([^;\n]*)`)
	shellAssignment = regexp.MustCompile(`(?:^|[\s;&|(])([A-Za-z_][A-Za-z0-9_]*)=([^\s;&|)]*)`)
	inputSetsName   = regexp.MustCompile(`(?:^|[\s;&|(])(?:read|readarray|mapfile|getopts)\s|printf\s+-v`)
)

// shownNames returns the variables of a line that hold only what the line
// shows: the variable of a for loop over plain words, and one assigned plain
// text. A value with a $ or a backtick is not shown, and neither is any
// variable of a line that reads one from input.
func shownNames(line string) map[string]bool {
	shown := map[string]bool{}
	if inputSetsName.MatchString(line) {
		return shown
	}
	for _, loop := range loopHeaderWords.FindAllStringSubmatch(line, -1) {
		shown[loop[1]] = !strings.ContainsAny(loop[2], "$`")
	}
	assignments := shellAssignment.FindAllStringSubmatch(line, -1)
	for _, assignment := range assignments {
		if _, seen := shown[assignment[1]]; !seen {
			shown[assignment[1]] = true
		}
	}
	for _, assignment := range assignments {
		if strings.ContainsAny(assignment[2], "$`") {
			shown[assignment[1]] = false
		}
	}
	return shown
}

// partBuildsPath reads a command for a word that expansionBuildsPath finds.
func partBuildsPath(part string, shown map[string]bool) bool {
	for i, word := range strings.Fields(maskLiterals(part)) {
		if wordBuildsPath(word, i == 0, shown) {
			return true
		}
	}
	return false
}

// maskLiterals writes a command with its quotes gone, and with text that the
// shell does not expand as x, so that a $ in it starts nothing and a blank in
// quotes ends no word. A / in single quotes stays, as it is part of the path.
func maskLiterals(part string) string {
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(part); i++ {
		c := part[i]
		switch {
		case c == '\\' && quote != '\'':
			i++
			b.WriteByte('x')
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '\'' || c == '"'):
			quote = c
		case quote == '\'' && c != '/', quote != 0 && strings.IndexByte(" \t\r\n", c) >= 0:
			b.WriteByte('x')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// wordBuildsPath reports whether one masked word holds $IFS, or is no program
// and starts with an expansion, holds a / and uses a variable the line does
// not show.
func wordBuildsPath(word string, program bool, shown map[string]bool) bool {
	hidden, split := false, false
	for i := 0; i+1 < len(word); i++ {
		if word[i] == '$' && (isNameByte(word[i+1]) || strings.IndexByte("{(@*#?!$-", word[i+1]) >= 0) {
			hidden = hidden || !shown[expansionName(word[i+1:])]
			split = split || namesIFS(word[i+1:])
		}
	}
	return split || !program && hidden && word[0] == '$' && strings.Contains(word, "/")
}

// expansionName returns the variable that the text after a $ names plainly, as
// $NAME or ${NAME}, or "" for any other expansion.
func expansionName(text string) string {
	braced, isBraced := strings.CutPrefix(text, "{")
	if isBraced {
		text = braced
	}
	end := 0
	for end < len(text) && isNameByte(text[end]) {
		end++
	}
	if isBraced && (end == len(text) || text[end] != '}') {
		return ""
	}
	return text[:end]
}

// namesIFS reports whether the text after a $ names the variable IFS.
func namesIFS(text string) bool {
	if strings.HasPrefix(text, "{IFS}") {
		return true
	}
	rest, found := strings.CutPrefix(text, "IFS")
	return found && (rest == "" || !isNameByte(rest[0]))
}

// withoutHeredocBodies returns a line without the bodies of its here-documents,
// which are data and no commands. A body it cannot place stays in the line.
func withoutHeredocBodies(command string) string {
	var b strings.Builder
	var pending []heredoc
	quote := byte(0)
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\\':
			b.WriteByte(c)
			i++
			if i == len(command) {
				return b.String()
			}
			c = command[i]
		case c == '\'' || c == '"':
			quote = c
		case strings.HasPrefix(command[i:], "<<<"):
			b.WriteString("<<")
			i += 2
			c = '<'
		case strings.HasPrefix(command[i:], "<<"):
			if h, end, found := heredocStart(command, i); found {
				pending = append(pending, h)
				b.WriteString(command[i:end])
				i = end - 1
				continue
			}
		case c == '\n' && len(pending) > 0:
			next, found := bodiesEnd(command, i+1, pending)
			if !found {
				return command
			}
			pending = nil
			b.WriteByte(c)
			i = next - 1
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// bodiesEnd returns the index after the bodies of pending, which start at
// command[start], and false when one has no closing line.
func bodiesEnd(command string, start int, pending []heredoc) (int, bool) {
	for _, h := range pending {
		next, found := heredocBody(command, start, h)
		if !found {
			return 0, false
		}
		start = next
	}
	return start, true
}

// posixReadParts is shellParts for the read checks. A line with $'...' or a
// brace expansion is uncertain too: the shell passes on other words than the
// parts hold.
func posixReadParts(command string) ([]string, bool) {
	parts, uncertain := shellParts(command, true)
	return parts, uncertain || shellExpansion(command)
}

// shellExpansion reports whether a POSIX command line holds bash's $'...' or
// $"..." quoting, or a brace expansion: a { outside quotes whose text holds a
// , or .. outside quotes before a blank or shell syntax ends it. Either turns
// the text into other words, which the gate does not work out, so the line
// needs the person. Inside quotes neither is expanded, and ${...} names a
// parameter. A command substitution, even inside double quotes, starts its
// own quoting.
func shellExpansion(command string) bool {
	stack := []byte{0}
	for i := 0; i < len(command); i++ {
		c := command[i]
		top := stack[len(stack)-1]
		next := byte(0)
		if i+1 < len(command) {
			next = command[i+1]
		}
		switch {
		case top == frameSingle:
			if c == '\'' {
				stack = stack[:len(stack)-1]
			}
		case c == '\\':
			// The escaped character is literal text.
			i++
		case top == frameDouble:
			switch {
			case c == '"':
				stack = stack[:len(stack)-1]
			case c == '$' && next == '(':
				stack = append(stack, frameParen)
				i++
			case c == '`':
				stack = append(stack, frameTick)
			}
		default:
			var found bool
			stack, i, found = unquotedExpansion(stack, command, i)
			if found {
				return true
			}
		}
	}
	return false
}

// unquotedExpansion reads command[i] outside quotes for shellExpansion. It
// returns the new stack, the index of the last byte it used, and whether the
// byte starts $'...' or $"..." or is a , or .. of a brace expansion.
func unquotedExpansion(stack []byte, command string, i int) ([]byte, int, bool) {
	c, top := command[i], stack[len(stack)-1]
	next := byte(0)
	if i+1 < len(command) {
		next = command[i+1]
	}
	switch {
	case c == '$':
		return dollarExpansion(stack, next, i)
	case (c == ',' || c == '.' && next == '.') && top == frameBrace:
		return stack, i, true
	case strings.IndexByte(" \t\r\n;|&<>", c) >= 0:
		// A blank or shell syntax ends a brace word, as in { a, b; }.
		for len(stack) > 1 && stack[len(stack)-1] == frameBrace {
			stack = stack[:len(stack)-1]
		}
		return stack, i, false
	}
	return unquotedFrame(stack, c), i, false
}

// dollarExpansion reads a $ outside quotes, with next the byte after it.
func dollarExpansion(stack []byte, next byte, i int) ([]byte, int, bool) {
	switch next {
	case '\'', '"':
		return stack, i, true
	case '$':
		// $$ is the shell's process id; a quote after it is a plain quote.
		return stack, i + 1, false
	case '{':
		return append(stack, frameParam), i + 1, false
	case '(':
		return append(stack, frameParen), i + 1, false
	}
	return stack, i, false
}

// unquotedFrame opens or closes the frame that c, outside quotes, starts or
// ends: a quote, a backquote, a parenthesis or a brace.
func unquotedFrame(stack []byte, c byte) []byte {
	top := stack[len(stack)-1]
	switch {
	case c == '\'' || c == '"' || c == '(':
		return append(stack, c)
	case c == '{':
		return append(stack, frameBrace)
	case c == '`' && top != frameTick:
		return append(stack, frameTick)
	case c == '`' || c == '}' && (top == frameBrace || top == frameParam):
		return stack[:len(stack)-1]
	case c == ')':
		return closeParen(stack)
	}
	return stack
}

// closeParen ends any brace word at a ), then the parenthesis or
// substitution that the ) closes.
func closeParen(stack []byte) []byte {
	for len(stack) > 1 && stack[len(stack)-1] == frameBrace {
		stack = stack[:len(stack)-1]
	}
	if len(stack) > 1 && stack[len(stack)-1] == frameParen {
		stack = stack[:len(stack)-1]
	}
	return stack
}

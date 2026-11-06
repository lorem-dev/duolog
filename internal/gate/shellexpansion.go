package gate

import "strings"

// Frames of the shell context that shellExpansion tracks.
const (
	frameSingle = '\''
	frameDouble = '"'
	frameParen  = '('
	frameTick   = '`'
	frameParam  = 'p'
	frameBrace  = 'b'
)

// unexpandedLine reports whether an exec action's line holds an expansion
// that shellExpansion finds. PowerShell and cmd have neither.
func unexpandedLine(a Action) bool {
	return a.Kind == "exec" && a.Shell != "powershell" && a.Shell != "windows" && shellExpansion(a.Target)
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

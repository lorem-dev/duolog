package gate

import "strings"

// scriptWords splits a part into the words the shell passes on: quotes group
// text and go away, and a backslash keeps the next character. It expands
// nothing. A redirect and the file it names are left out, since the redirect
// checks judge that file.
func scriptWords(part string) []scriptWord {
	var words []scriptWord
	drop := false
	for _, word := range splitScriptWords(part) {
		switch {
		case drop:
			drop = false
		case word.redirect:
			// The file is the next word when the operator stands alone.
			drop = strings.Trim(word.text, "0123456789<>&|-") == ""
		default:
			words = append(words, word)
		}
	}
	return words
}

// scriptWord is one word of scriptWords. redirect means it starts with an
// unquoted redirect operator, and glob that it holds a ?, * or [ outside
// quotes and not escaped, so the shell expands it, even when other parts of
// the word are quoted.
type scriptWord struct {
	text           string
	redirect, glob bool
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
			current.glob = current.glob || strings.IndexByte("*?[", c) >= 0
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

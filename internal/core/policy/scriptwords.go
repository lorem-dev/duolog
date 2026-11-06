package policy

import "strings"

// scriptWords splits a part into the words the shell passes on: quotes group
// text and go away, and a backslash keeps the next character, which inside
// double quotes it does only before $, `, " and \. A backslash before a
// newline goes with it. It expands nothing. A redirect and the file it names
// are left out, since the redirect checks judge that file.
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
	inWord := false
	for i := 0; i < len(part); i++ {
		c := part[i]
		switch {
		case c == '\\' && quote != '\'':
			text, last, word := scriptBackslash(part, i, quote)
			b.WriteString(text)
			i, inWord = last, inWord || word
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

// scriptBackslash reads the backslash at part[i], outside single quotes. It
// returns the text it stands for, the index of the last byte it used, and
// whether it is part of a word.
func scriptBackslash(part string, i int, quote byte) (text string, last int, word bool) {
	switch {
	case i+1 < len(part) && part[i+1] == '\n':
		// A line continuation: the shell removes both.
		return "", i + 1, false
	case quote == '"' && (i+1 == len(part) || strings.IndexByte("$`\"\\", part[i+1]) < 0):
		// Inside double quotes a backslash escapes only $, `, " and \;
		// before anything else it stays, as awk's \057 needs.
		return `\`, i, true
	case i+1 < len(part):
		return part[i+1 : i+2], i + 1, true
	}
	return "", i, true
}

// redirectAt reports whether text starts with a redirect operator, after an
// optional stream number.
func redirectAt(text string) bool {
	operator := strings.TrimLeft(text, "0123456789")
	return strings.HasPrefix(operator, "<") || strings.HasPrefix(operator, ">") || strings.HasPrefix(operator, "&>")
}

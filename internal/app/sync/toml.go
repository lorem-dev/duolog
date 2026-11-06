package agentsync

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// tomlBasic writes a single-line TOML basic string.
func tomlBasic(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("value is not valid UTF-8")
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		writeEscaped(&b, r, true)
	}
	b.WriteByte('"')
	return b.String(), nil
}

// tomlMultiline writes a multi-line TOML basic string. Every quote and
// backslash is escaped, so no body can close the string early. Spaces and tabs
// before a line break are escaped, so no output line ends in whitespace.
func tomlMultiline(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("value is not valid UTF-8")
	}
	var b strings.Builder
	b.WriteString("\"\"\"\n")
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == ' ' || r == '\t' {
			j := i
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
			if j < len(runes) && runes[j] == '\n' {
				for ; i < j; i++ {
					if runes[i] == ' ' {
						b.WriteString("\\u0020")
					} else {
						b.WriteString(`\t`)
					}
				}
				i = j - 1
				continue
			}
		}
		if r == '\n' {
			b.WriteByte('\n')
			continue
		}
		writeEscaped(&b, r, false)
	}
	b.WriteString("\"\"\"")
	return b.String(), nil
}

func writeEscaped(b *strings.Builder, r rune, singleLine bool) {
	switch {
	case r == '"':
		b.WriteString(`\"`)
	case r == '\\':
		b.WriteString(`\\`)
	case r == '\t':
		b.WriteString(`\t`)
	case r == '\n' && singleLine:
		b.WriteString(`\n`)
	case r == '\r':
		b.WriteString(`\r`)
	case r < 0x20 || r == 0x7f:
		fmt.Fprintf(b, `\u%04X`, r)
	default:
		b.WriteRune(r)
	}
}

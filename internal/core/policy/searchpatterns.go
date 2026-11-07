package policy

import (
	"slices"
	"strings"
)

// searchSyntax describes the options of a search program, so that the word
// it takes as its pattern is known. Only a sure reading finds the pattern:
// an unknown option, or one whose value may or may not be the next word,
// leaves every word to be judged as a path.
type searchSyntax struct {
	plainShorts string
	// valueShorts take a value, the rest of the word or the next word;
	// unsureShorts take one that some versions want in the same word.
	valueShorts, unsureShorts string
	plainLong, valueLong      []string
	unsureLong                []string
	// The values of these options are patterns, not files.
	patternShorts string
	patternLong   []string
	// These give the patterns, so that every operand is a file.
	explicitShorts string
	explicitLong   []string
	// These name a file in their value.
	fileShorts string
	fileLong   []string
	// With these there is no pattern operand.
	noPattern []string
	// Git stops reading options at the first operand.
	stopAtOperand bool
}

var (
	grepSyntax = searchSyntax{
		plainShorts: checkedOptions["grep"].plainShorts, valueShorts: "ABDdefm", unsureShorts: "C",
		plainLong:  append(slices.Clip(checkedOptions["grep"].plainLong), checkedOptions["grep"].attachedLong...),
		valueLong:  grepSeparateLong,
		unsureLong: []string{"--context"},
		// grep -e and --regexp give a pattern; -f, --file and --exclude-from name a file.
		patternShorts: "e", patternLong: []string{"--regexp"},
		explicitShorts: "ef", explicitLong: []string{"--regexp", "--file"},
		fileShorts: "f", fileLong: []string{"--file", "--exclude-from"},
	}
	rgSyntax = searchSyntax{
		plainShorts: rgPlainShorts, valueShorts: rgValueShorts,
		plainLong: slices.DeleteFunc(slices.Clone(rgLongOptions), func(name string) bool { return slices.Contains(rgSeparateLong, name) }),
		valueLong: rgSeparateLong,
		// rg -r and --replace give the text that replaces a match.
		patternShorts: "er", patternLong: []string{"--regexp", "--replace"},
		explicitShorts: "ef", explicitLong: []string{"--regexp", "--file"},
		fileShorts: "f", fileLong: []string{"--file"},
		noPattern: []string{"--files", "--type-list"},
	}
	gitGrepSyntax = searchSyntax{
		plainShorts: "aiIrwvhHEGFPnlLzocpWq0123456789", valueShorts: "ABCmef",
		plainLong: []string{"--cached", "--no-index", "--untracked", "--no-exclude-standard", "--exclude-standard", "--recurse-submodules", "--text", "--textconv",
			"--no-textconv", "--ignore-case", "--recursive", "--no-recursive", "--word-regexp", "--invert-match", "--full-name", "--extended-regexp", "--basic-regexp",
			"--fixed-strings", "--perl-regexp", "--line-number", "--column", "--files-with-matches", "--name-only", "--files-without-match", "--null", "--only-matching",
			"--count", "--color", "--no-color", "--break", "--heading", "--show-function", "--function-context", "--and", "--or", "--not", "--all-match", "--quiet"},
		valueLong:     []string{"--max-depth", "--threads", "--context", "--after-context", "--before-context", "--max-count"},
		patternShorts: "e", explicitShorts: "ef", fileShorts: "f",
		stopAtOperand: true,
	}
)

// searchOption is what one option word of a search command means.
type searchOption struct {
	known, explicit bool
	// pattern: the option's value is a pattern; inWord: it is written in the
	// word itself.
	pattern, inWord bool
	// next is 1 when the value is the next word, and -1 when it may be.
	next int
	// file is a file named in the word, as in -f../x.
	file      string
	noPattern bool
}

// option reads one option word.
func (s searchSyntax) option(word string) searchOption {
	if strings.HasPrefix(word, "--") {
		return s.longOption(word)
	}
	for i, letter := range word[1:] {
		value := word[i+2:]
		switch {
		case strings.ContainsRune(s.valueShorts+s.unsureShorts, letter):
			o := searchOption{known: true, explicit: strings.ContainsRune(s.explicitShorts, letter), pattern: strings.ContainsRune(s.patternShorts, letter), inWord: value != ""}
			if strings.ContainsRune(s.fileShorts, letter) {
				o.file = value
			}
			if value == "" {
				o.next = 1
				if strings.ContainsRune(s.unsureShorts, letter) {
					o.next = -1
				}
			}
			return o
		case !strings.ContainsRune(s.plainShorts, letter):
			return searchOption{}
		}
	}
	return searchOption{known: true}
}

func (s searchSyntax) longOption(word string) searchOption {
	name, value, attached := strings.Cut(word, "=")
	o := searchOption{
		known:     slices.Contains(s.plainLong, name) || slices.Contains(s.valueLong, name) || slices.Contains(s.unsureLong, name),
		explicit:  slices.Contains(s.explicitLong, name),
		pattern:   slices.Contains(s.patternLong, name),
		inWord:    attached,
		noPattern: slices.Contains(s.noPattern, name),
	}
	if slices.Contains(s.fileLong, name) {
		o.file = value
	}
	switch {
	case attached:
	case slices.Contains(s.valueLong, name):
		o.next = 1
	case slices.Contains(s.unsureLong, name):
		o.next = -1
	}
	return o
}

// patternScan holds what patterns has read so far.
type patternScan struct {
	syntax searchSyntax
	skip   map[int]bool
	files  []string
	// first is the index of the first operand, or -1.
	first                   int
	sure, explicit, literal bool
}

// optionWord reads the option at words[i] and returns the index of the last
// word it used.
func (p *patternScan) optionWord(words []string, i int) int {
	o := p.syntax.option(words[i])
	p.sure = p.sure && o.known && !o.noPattern && (o.next >= 0 || p.first >= 0)
	p.explicit = p.explicit || o.explicit
	if o.file != "" {
		p.files = append(p.files, o.file)
	}
	p.skip[i] = o.pattern && o.inWord
	if o.next == 1 && i+1 < len(words) {
		i++
		p.skip[i] = o.pattern
	}
	return i
}

// patterns returns which of words, from start on, are patterns rather than
// files, and the files that options name inside a word. The pattern operand
// is the first operand, unless an option gives the patterns.
func (s searchSyntax) patterns(words []string, start int) (map[int]bool, []string) {
	p := patternScan{syntax: s, skip: map[int]bool{}, first: -1, sure: true}
	for i := start; i < len(words) && (!s.stopAtOperand || p.first < 0); i++ {
		word := words[i]
		switch {
		case p.literal || word == "-" || !strings.HasPrefix(word, "-"):
			if p.first < 0 {
				p.first = i
			}
		case word == "--":
			p.literal = true
			if s.stopAtOperand {
				// Git reads only paths after --.
				i = len(words)
			}
		default:
			i = p.optionWord(words, i)
		}
	}
	if p.sure && !p.explicit && p.first >= 0 {
		p.skip[p.first] = true
	}
	return p.skip, p.files
}

// searchPatterns returns which words of a command, its program at words[0],
// are a search pattern, replacement or program text rather than a file, and
// the files that options name inside a word, such as the ../x of -f../x.
func searchPatterns(words []string) (map[int]bool, []string) {
	switch programName(words[0]) {
	case "grep", "egrep", "fgrep":
		return grepSyntax.patterns(words, 1)
	case "rg":
		return rgSyntax.patterns(words, 1)
	case "git":
		return gitPatterns(words)
	case "tr":
		// tr reads standard input only; its words are sets of characters.
		skip := map[int]bool{}
		for i := 1; i < len(words); i++ {
			skip[i] = true
		}
		return skip, nil
	case "awk":
		if program, ok := plainAwkProgram(words[1:]); ok {
			return map[int]bool{program + 1: true}, nil
		}
	}
	return nil, nil
}

// searchWords reads a grep or rg command of a POSIX line, its program at
// command[0] and its words as written, for its search pattern, which is no
// path. named is true when an option names a file outside the work root in
// its own word, as -f../x does.
func searchWords(a Action, command []string) (patterns map[int]bool, named bool) {
	if !posixShell(a) || !slices.Contains([]string{"grep", "egrep", "fgrep", "rg"}, programName(command[0])) {
		return nil, false
	}
	trimmed := make([]string, len(command))
	for i, word := range command {
		trimmed[i] = strings.Trim(withoutEscapes(a, word), "'\"")
	}
	patterns, files := searchPatterns(trimmed)
	return patterns, slices.ContainsFunc(files, func(file string) bool { return outsideOperand(a, file) })
}

// expandsWords reports whether a word of a command, its program at
// words[0] and its words as written, may expand to an option once the shell
// reads it, so that no word can be known to be a search pattern or the value
// of a pattern option: grep /x $P with P=-ex reads /x as a file. Such a word
// holds a $ that starts an expansion outside single quotes, or a glob
// character, *, ? or [, or a ~ outside quotes.
func expandsWords(words []string) bool {
	return len(words) > 1 && mayExpand(strings.Join(words[1:], " "))
}

// surePatterns returns program and patterns, the program of a command and
// the words of it that are a search pattern, as the gate may use them: when a
// word of the command, its words as written, may expand to an option
// (expandsWords), no word is a pattern, and a program with pattern options
// becomes "", so that the value of none is skipped.
func surePatterns(words []string, program string, patterns map[int]bool) (string, map[int]bool) {
	if !expandsWords(words) {
		return program, patterns
	}
	if _, known := patternOptions[program]; known {
		program = ""
	}
	return program, nil
}

// mayExpand reports whether text, as written, holds a $ that starts an
// expansion outside single quotes, or a *, ?, [ or ~ outside quotes.
func mayExpand(text string) bool {
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case c == '\\':
			i++ // The next character is escaped.
		case c == '$':
			if i+1 < len(text) && (isNameByte(text[i+1]) || strings.IndexByte("{(@*#?!$-", text[i+1]) >= 0) {
				return true
			}
		case quote == '"':
			if c == '"' {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case strings.IndexByte("*?[~", c) >= 0:
			return true
		}
	}
	return false
}

// isNameByte reports whether c may stand in a shell variable name, or be a
// positional parameter.
func isNameByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// gitPatterns finds the pattern of git grep, and the -G and -S values of the
// commands that search the history.
func gitPatterns(words []string) (map[int]bool, []string) {
	start := 1
	for start < len(words) && strings.HasPrefix(words[start], "-") {
		if slices.Contains([]string{"-C", "--git-dir", "--work-tree", "--namespace"}, words[start]) {
			start++
		}
		start++
	}
	if start >= len(words) {
		return nil, nil
	}
	switch words[start] {
	case "grep":
		return gitGrepSyntax.patterns(words, start+1)
	case "log", "diff", "show":
		skip := map[int]bool{}
		for i := start + 1; i+1 < len(words) && words[i] != "--"; i++ {
			if words[i] == "-G" || words[i] == "-S" {
				i++
				skip[i] = true
			}
		}
		return skip, nil
	}
	return nil, nil
}

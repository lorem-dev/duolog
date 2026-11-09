package policy

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
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

// patternEscapable are the characters before which a backslash in a double
// quoted rg pattern is allowed. The shell keeps such a backslash, so rg gets it.
const patternEscapable = "|()*;&"

// deniedInPattern are the names of the default read denies and of duolog state.
// A quoted pattern that names one is left to the plain quote rules.
var deniedInPattern = []string{".duolog", ".env", ".pem", ".key", "secrets"}

// rgPatternEscapes turns each allowed escape of patternEscapable into its
// character, so that quotedRunUnsafe finds only a backslash that is not allowed.
var rgPatternEscapes = strings.NewReplacer(`\|`, "|", `\(`, "(", `\)`, ")", `\*`, "*", `\;`, ";", `\&`, "&")

// shellEscape reports whether the backslash at command[i], inside the quote
// quote or none, escapes the next character: never in single quotes, and in
// double quotes not before one of patternEscapable, where the shell keeps it.
// A part that holds such a backslash stays uncertain unless rgQuotedPattern
// reads it, since literalQuotes refuses every backslash.
func shellEscape(command string, i int, quote byte) bool {
	if quote == '\'' {
		return false
	}
	return quote != '"' || i+1 == len(command) || strings.IndexByte(patternEscapable, command[i+1]) < 0
}

// readQuotes reports whether a read command's quotes only group literal text,
// with quoted spaces allowed and the search pattern of rg read as data.
func readQuotes(part string) bool {
	return literalQuotes(part, true) || rgQuotedPattern(part)
}

// rgQuotedPattern reports whether an rg command's quotes only group literal
// text once a * is allowed in the quoted text of its search pattern, and a
// backslash before one of patternEscapable in double quotes. Nothing else is
// widened: a pattern given by an option, a path and any other word keep the
// rules of literalQuotes, since the checks after this one read such a word as
// a glob. A $ or backtick in double quotes still counts, and so does a pattern
// that names a denied name.
func rgQuotedPattern(part string) bool {
	words := quotedWords(part)
	if len(words) < 2 || words[0] != "rg" {
		return false
	}
	// Without the pattern options, only the pattern operand is marked.
	syntax := rgSyntax
	syntax.patternShorts, syntax.patternLong = "", nil
	patterns, _ := syntax.patterns(words, 1)
	wordIndex, inWord := -1, false
	for i := 0; i < len(part); i++ {
		r, size := utf8.DecodeRuneInString(part[i:])
		if unicode.IsSpace(r) {
			inWord = false
			i += size - 1
			continue
		}
		if !inWord {
			wordIndex, inWord = wordIndex+1, true
		}
		c := part[i]
		if c != '\'' && c != '"' {
			if strings.IndexByte("\\$`<>()", c) >= 0 {
				return false
			}
			continue
		}
		end := strings.IndexByte(part[i+1:], c)
		if end <= 0 || !quotedRunData(part[i+1:i+1+end], c, patterns[wordIndex] && !namesDenied(words[wordIndex])) {
			return false
		}
		i += end + 1
	}
	return true
}

// quotedRunData reports whether a quoted run of rgQuotedPattern, in the quote
// quote, holds no text the shell would read differently. pattern allows what
// the search pattern may hold.
func quotedRunData(run string, quote byte, pattern bool) bool {
	unsafe := strings.Replace(quotedLiteralUnsafe, " ", "", 1)
	if quote == '"' {
		unsafe += "$`"
	}
	if !pattern {
		return !quotedRunUnsafe(run, unsafe)
	}
	if quote == '"' {
		run = rgPatternEscapes.Replace(run)
	}
	return !quotedRunUnsafe(run, strings.Replace(unsafe, "*", "", 1))
}

// namesDenied reports whether text names one of deniedInPattern, in any case.
func namesDenied(text string) bool {
	lower := strings.ToLower(text)
	return slices.ContainsFunc(deniedInPattern, func(name string) bool { return strings.Contains(lower, name) })
}

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
// reads it, or shift which word is a pattern, so that no word can be known to
// be a search pattern or the value of a pattern option: grep /x $P with
// P=-ex reads /x as a file, and grep -A 1* /x f with files 10 and 11 makes 11
// the pattern and /x a file. Such a word holds a $ that starts an expansion
// outside single quotes, or a glob character, *, ? or [, or a ~ outside
// quotes. A glob alone, after every pattern word of patterns and not the
// value of a pattern option of program, whose word starts with a plain
// character, expands only to names that start with it: never to an option,
// so it changes no pattern, as in grep -c '\bLoad\(' internal/*_test.go.
// The gate judges it as a path as written.
func expandsWords(words []string, program string, patterns map[int]bool) bool {
	if len(words) < 2 {
		return false
	}
	dollar, glob, _ := expansions(strings.Join(words[1:], " "))
	if dollar || !glob {
		return dollar
	}
	last := 0
	for i, pattern := range patterns {
		if pattern {
			last = max(last, i)
		}
	}
	for i := 1; i < len(words); i++ {
		_, glob, closed := expansions(words[i])
		if !closed {
			// A quote that spans words leaves each word's reading unsure.
			return true
		}
		if !glob {
			continue
		}
		if skip, next := skipsPatternValue(program, strings.Trim(words[i-1], `'"`)); i <= last || skip && next || !plainStart(words[i]) {
			return true
		}
	}
	return false
}

// shellGlobInRead reports whether a POSIX line holds a command, other than
// find, with an argument that has a *, ? or [ outside quotes. The shell
// expands such a word before the program runs, so a glob where a command
// takes its pattern can become several words, and every word after the first
// is a file that the program reads: grep /x/pa* f reads /x/passwd- when
// /x/passwd comes first. A glob that names a path reaches the same reading
// through the read rules. find cannot read this way, since it refuses a path
// after its expression.
func shellGlobInRead(a Action) bool {
	return a.Shell != "powershell" && a.Shell != "windows" && slices.ContainsFunc(rawExecParts(a.Target), func(part string) bool {
		words, _ := shellWords(commandAfterKeywords(part))
		return len(words) > 1 && programName(words[0].text) != "find" && slices.ContainsFunc(words[1:], shellWord.hasGlob)
	})
}

// readGlobWords returns the words of a POSIX line's read commands that hold a
// *, ? or [ outside quotes, wherever the command takes them. The shell expands
// such a word before the program runs, so even a glob where the program takes
// a pattern can become files, and each word gets the decision of a glob in a
// path: grep .e* f reads .env when .editorconfig comes first. find is left out
// for the reason shellGlobInRead gives, and outside Git a word that starts with
// - because it is an option's own value, as --include=*.go is. On Git such a
// word counts (gitOptionGlob). The value of a glob option written apart counts
// only when the shell can make more than one word of it (globStaysOneWord), or
// when it can name state. The command is judged by its words as the shell
// passes them on, so 'grep' is grep.
func readGlobWords(a Action) []string {
	var globs []string
	budget := newGlobBudget()
	for _, words := range readCommandWords(a) {
		program := programName(words[0].text)
		literal := false
		for i, word := range words[1:] {
			literal = literal || word.text == "--"
			if !word.hasGlob() || program != "git" && strings.HasPrefix(word.text, "-") {
				continue
			}
			value := !literal && separateGlobOption(program, words[i].text)
			if !value || statePattern(workTarget(a, word.text)) || !globStaysOneWord(a, word.text, budget) {
				globs = append(globs, word.text)
			}
		}
	}
	return globs
}

// readCommandWords returns the words of each read command of a POSIX line that
// has an argument, as the shell passes them on, find left out.
func readCommandWords(a Action) [][]shellWord {
	if a.Shell == "powershell" || a.Shell == "windows" {
		return nil
	}
	var commands [][]shellWord
	for _, part := range rawExecParts(a.Target) {
		words, _ := shellWords(commandAfterKeywords(part))
		if len(words) > 1 && programName(words[0].text) != "find" && readOnlyCommand(wordsLine(words)) {
			commands = append(commands, words)
		}
	}
	return commands
}

// wordsLine writes parsed words as one line of a part, each word whole.
func wordsLine(words []shellWord) string {
	texts := make([]string, len(words))
	for i, word := range words {
		texts[i] = strings.ReplaceAll(word.text, " ", quotedSpace)
	}
	return strings.Join(texts, " ")
}

// unmatchedDenied reports whether on_unmatched denies a part of the line, one
// that no rule takes: an ask must not stand in for that deny.
func unmatchedDenied(p config.Policy, a Action, parts []string) bool {
	return p.OnUnmatched == Deny && slices.ContainsFunc(parts, func(part string) bool {
		partAction := a
		partAction.Target = part
		_, ruled := firstRule(p, a, partAction)
		return !ruled
	})
}

// readGlobAsk is the decision that the globs of a's read commands give the
// line, nil for none, and denied is true when it is a deny: a pathspec glob
// aimed at a denied name, or an unquoted glob that can name duolog state, is
// denied wherever it stands, and an option-shaped glob on Git where only sure
// reads may run. Any other unquoted glob asks, unless on_unmatched denies the
// line.
func readGlobAsk(p config.Policy, lead LeadRules, a Action, m mode.Mode, parts []string) (asked *Decision, denied bool) {
	if unquotedRgGlob(a) {
		asked = reasonAsk(a, m, "a shell glob in an rg option can expand onto denied files; quote the glob")
	}
	if gitGlobDenied(p, lead, a, parts) {
		return &Decision{Kind: Deny, Reason: "a Git pathspec glob can match a file that policy denies; name the files", Rule: "read command", Mode: m}, true
	}
	if d := gitOptionGlobDeny(p, lead, a, m); d != nil {
		return d, true
	}
	if d := wrappedReadDeny(p, lead, m, a); d != nil {
		return d, true
	}
	for _, glob := range readGlobWords(a) {
		if statePattern(workTarget(a, glob)) {
			return &Decision{Kind: Deny, Reason: "duolog state is not readable by workers", Rule: RuleState, Mode: m}, true
		}
		if asked == nil && !unmatchedDenied(p, a, parts) {
			asked = reasonAsk(a, m, "a shell glob can expand onto files that policy denies; search for a pattern without * ? [ or name the files")
		}
	}
	return asked, false
}

// hasGlob reports whether a word holds a *, ? or [ outside quotes.
func (w shellWord) hasGlob() bool {
	for i := 0; i < len(w.text); i++ {
		if !w.quoted[i] && strings.IndexByte("*?[", w.text[i]) >= 0 {
			return true
		}
	}
	return false
}

// plainStart reports whether word, as written, starts with a character that
// the shell keeps as it is: a letter, a digit, _, . or /.
func plainStart(word string) bool {
	if word == "" {
		return false
	}
	c := word[0]
	return isNameByte(c) || c == '.' || c == '/'
}

// surePatterns returns program and patterns, the program of a command and
// the words of it that are a search pattern, as the gate may use them: when a
// word of the command, its words as written, may expand to an option
// (expandsWords), no word is a pattern, and a program with pattern options
// becomes "", so that the value of none is skipped.
func surePatterns(words []string, program string, patterns map[int]bool) (string, map[int]bool) {
	if !expandsWords(words, program, patterns) {
		return program, patterns
	}
	if _, known := patternOptions[program]; known {
		program = ""
	}
	return program, nil
}

// expansions reads text as written: dollar is true when it holds a $ that
// starts an expansion outside single quotes, glob when it holds a *, ?, [ or
// ~ outside quotes, and closed when every quote it opens ends in it.
func expansions(text string) (dollar, glob, closed bool) {
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
				dollar = true
			}
		case quote == '"':
			if c == '"' {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case strings.IndexByte("*?[~", c) >= 0:
			glob = true
		}
	}
	return dollar, glob, quote == 0
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

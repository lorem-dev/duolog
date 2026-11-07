package policy

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// lastWrapper returns the wrapper right in front of the program of a
// command's words, or "" when the program stands first, after assignments.
func lastWrapper(words []string) string {
	last := ""
	for i := 0; i < len(words); {
		word := strings.Trim(words[i], "'\"")
		if execAssignment.MatchString(word) {
			i++
			continue
		}
		wrapper := filepath.Base(word)
		if !slices.Contains(wrapperNames, wrapper) {
			return last
		}
		last = wrapper
		i = skipWrapperOptions(wrapper, words, i+1, nil)
		if wrapper == "timeout" && i < len(words) {
			i++
		}
	}
	return last
}

// assignedSubstitution returns the index of the first assignment among the
// leading assignments of a part's words that holds a command substitution, as
// x=$(cat /x) does, or -1. The substitution runs a command whose words follow
// it, so wordsOutside judges every later word as a path.
func assignedSubstitution(words []string) int {
	for i := 0; i < len(words) && execAssignment.MatchString(words[i]); i++ {
		if strings.Contains(words[i], "$(") || strings.Contains(words[i], "`") {
			return i
		}
	}
	return -1
}

// redirectsOutside reports whether a redirect of a part names a file outside
// the work root. A redirect can stand anywhere, even between an option and
// its value, so its file is judged on its own and the redirect leaves the
// words before any of them is taken as an option's value. A file that an
// output redirect writes in the worker's own Claude session temp folder is
// the worker's own; a file that an input redirect reads may feed a program
// that runs it, so it is not.
func redirectsOutside(a Action, part string) bool {
	writes := slices.ContainsFunc(redirectTargets(part), func(target string) bool {
		return (outsideOperand(a, target) || outsideOperand(a, unquoted.Replace(target))) && !ownTempWord(a, target, tempRedirect)
	})
	return writes || slices.ContainsFunc(inputTargets(part), func(target string) bool {
		return outsideOperand(a, target) || outsideOperand(a, unquoted.Replace(target))
	})
}

// splitQuotes reports whether the words of a command, split at blanks, may
// not be the words the shell passes on: a quote is left open in a word, a
// word ends in a backslash that escapes the blank after it, or a quoted word
// starts with -, which the program reads as an option.
func splitQuotes(words []string) bool {
	return slices.ContainsFunc(words, func(word string) bool {
		quoted := strings.ContainsAny(word, `'"`)
		return strings.Count(word, "'")%2 == 1 || strings.Count(word, `"`)%2 == 1 || strings.HasSuffix(word, `\`) ||
			quoted && strings.HasPrefix(unquoted.Replace(word), "-")
	})
}

// shellWord is one word of a command part as the shell passes it on, with
// quotes and escapes removed. raw keeps the backslashes that escaped a
// character outside quotes, as a Windows path is spelled. quoted marks each
// byte of text that came from inside quotes or after a backslash.
type shellWord struct {
	text, raw string
	quoted    []bool
}

// wordSplitter builds the words of shellWords.
type wordSplitter struct {
	words     []shellWord
	text, raw strings.Builder
	quoted    []bool
	started   bool
	quote     byte
}

func (s *wordSplitter) add(c byte, quoted bool) {
	s.text.WriteByte(c)
	s.raw.WriteByte(c)
	s.quoted = append(s.quoted, quoted)
	s.started = true
}

func (s *wordSplitter) flush() {
	if s.started {
		s.words = append(s.words, shellWord{text: s.text.String(), raw: s.raw.String(), quoted: s.quoted})
	}
	s.text.Reset()
	s.raw.Reset()
	s.quoted, s.started = nil, false
}

// quotedByte reads part[i] inside quotes and returns the index of the last
// byte it used.
func (s *wordSplitter) quotedByte(part string, i int) int {
	c := part[i]
	switch {
	case c == s.quote:
		s.quote = 0
	case s.quote == '"' && c == '\\' && i+1 < len(part) && part[i+1] == '\n':
		// A backslash and a newline continue the line: both go.
		i++
	case s.quote == '"' && c == '\\' && i+1 < len(part) && strings.IndexByte("\"\\$`", part[i+1]) >= 0:
		// Inside double quotes a backslash escapes only these; before
		// anything else it stays.
		i++
		s.add(part[i], true)
	default:
		s.add(c, true)
	}
	return i
}

// shellWords splits a part into words by POSIX quoting. It returns false for
// a quote left open.
func shellWords(part string) ([]shellWord, bool) {
	var s wordSplitter
	for i := 0; i < len(part); i++ {
		c := part[i]
		switch {
		case s.quote != 0:
			i = s.quotedByte(part, i)
		case c == '\'' || c == '"':
			s.quote = c
			s.started = true
		case c == '\\' && i+1 < len(part) && part[i+1] == '\n':
			// A line continuation: the shell removes both.
			i++
		case c == '\\':
			if i+1 < len(part) {
				s.raw.WriteByte('\\')
				i++
				s.add(part[i], true)
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			s.flush()
		default:
			s.add(c, false)
		}
	}
	s.flush()
	return s.words, s.quote == 0
}

// redirectAt returns the index of the first < or > in a word that is not
// quoted, at or after from, or -1.
func (w shellWord) redirectAt(from int) int {
	for i := from; i < len(w.text); i++ {
		if (w.text[i] == '<' || w.text[i] == '>') && !w.quoted[i] {
			return i
		}
	}
	return -1
}

// upTo returns the text of a word from start up to its next redirect, and
// where that redirect starts, or -1.
func (w shellWord) upTo(start int) (string, int) {
	next := w.redirectAt(start)
	if next < 0 {
		return w.text[start:], -1
	}
	return w.text[start:next], next
}

// splitRedirects returns the words that the program receives and the files
// that redirects outside quotes name: the word after >, >>, >|, &>, N>, <
// or <>, attached or apart. A here-document or here-string names no file,
// nor does a redirect onto another stream (2>&1, <&-).
func splitRedirects(words []shellWord) (args []shellWord, files []string) {
	for i := 0; i < len(words); i++ {
		w := words[i]
		at := w.redirectAt(0)
		if at < 0 {
			args = append(args, w)
			continue
		}
		if prefix := w.text[:at]; prefix != "" && prefix != "&" && strings.Trim(prefix, "0123456789") != "" {
			args = append(args, shellWord{text: prefix, raw: prefix, quoted: w.quoted[:at]})
		}
		var named []string
		named, i = redirectFiles(words, i, at)
		files = append(files, named...)
	}
	return args, files
}

// redirectFiles reads the redirects that start at words[i][at:], and returns
// the files they name and the index of the last word they used.
func redirectFiles(words []shellWord, i, at int) ([]string, int) {
	var files []string
	w := words[i]
	for at >= 0 {
		end := at
		for end < len(w.text) && !w.quoted[end] && strings.IndexByte("<>&|", w.text[end]) >= 0 {
			end++
		}
		operator := w.text[at:end]
		target, next := w.upTo(end)
		if target == "" && next < 0 && i+1 < len(words) {
			// The file is the next word, up to any redirect in it.
			i++
			w = words[i]
			target, next = w.upTo(0)
		}
		duplicate := strings.Contains(operator[1:], "&")
		if !strings.HasPrefix(operator, "<<") && target != "" && (!duplicate || target != "-" && strings.Trim(target, "0123456789") != "") {
			files = append(files, target)
		}
		at = next
	}
	return files, i
}

// verbatimPrograms run no argument as a command, so a quoted argument is one
// word and nothing in it is a path but the word as a whole. A program that
// can run a command it is given, such as sh -c, xargs or find -exec, keeps
// the word-by-word reading, which also judges the paths inside a quoted
// command.
var verbatimPrograms = []string{"grep", "egrep", "fgrep", "rg", "tr", "cat", "head", "tail", "sort", "uniq", "wc", "cut", "ls"}

// awkProgramUnsafe holds what lets an awk program run a command, read a file
// or write one: system(), getline, a redirect or pipe, and gawk's @load and
// @include.
var awkProgramUnsafe = regexp.MustCompile(`system|getline|[<>|@]`)

// verbatimCommand reports whether a command, its program at words[0], runs
// nothing it is given.
func verbatimCommand(words []string) bool {
	args := words[1:]
	switch programName(words[0]) {
	case "git":
		return verbatimGit(args)
	case "go":
		// go list runs no command but one named with -toolexec.
		return len(args) > 0 && args[0] == "list" && !slices.ContainsFunc(args, func(word string) bool {
			option := strings.TrimLeft(word, "-")
			return strings.HasPrefix(option, "toolexec") || strings.HasPrefix(option, "exec")
		})
	case "sed":
		return sedScripts(args) != nil
	case "awk":
		_, ok := plainAwkProgram(args)
		return ok
	}
	return slices.Contains(verbatimPrograms, programName(words[0]))
}

// verbatimGit reports whether a Git command only reads: git -c and its kind
// can turn any word into a command, and git grep -O runs the pager it names.
func verbatimGit(args []string) bool {
	return slices.Contains([]string{"grep", "log", "diff", "show", "status"}, gitSubcommand(args)) && !slices.ContainsFunc(args, func(word string) bool {
		return strings.HasPrefix(word, "--open-files-in-pager") || !strings.HasPrefix(word, "--") && strings.HasPrefix(word, "-") && strings.Contains(word, "O")
	})
}

// gitSubcommand returns the subcommand of a Git command after options that
// only name a folder or turn something off, or "" after any other option.
func gitSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		word := args[i]
		name, _, _ := strings.Cut(word, "=")
		switch {
		case !strings.HasPrefix(word, "-"):
			return word
		case word == "-C" || word == "--git-dir" || word == "--work-tree" || word == "--namespace":
			i++
		case slices.Contains([]string{"--git-dir", "--work-tree", "--namespace"}, name):
		case !slices.Contains([]string{"--no-pager", "-P", "--no-replace-objects", "--literal-pathspecs", "--no-optional-locks"}, word):
			return ""
		}
	}
	return ""
}

// sedSubstitution reports whether a sed script is one s command whose flags
// print or match only: no w to write a file and no e to run the pattern space.
func sedSubstitution(script string) bool {
	if len(script) < 4 || script[0] != 's' || script[1] == '\\' || script[1] == '\n' {
		return false
	}
	delimiter, ends := script[1], 0
	i := 2
	for ; i < len(script) && ends < 2; i++ {
		switch script[i] {
		case '\\':
			i++
		case '\n':
			return false
		case delimiter:
			ends++
		}
	}
	return ends == 2 && strings.Trim(script[i:], "gpiIm0123456789") == ""
}

// sedScripts returns the indices of a sed command's scripts when each one is
// a print range or an s command that cannot write or run anything, and the
// options only change how it reads; otherwise nil.
func sedScripts(args []string) []int {
	scripts, ok := sedScriptIndices(args)
	if !ok || len(scripts) == 0 {
		return nil
	}
	for _, i := range scripts {
		if i >= len(args) || !sedSubstitution(args[i]) && !sedPrintRange.MatchString(args[i]) {
			return nil
		}
	}
	return scripts
}

// sedScriptIndices finds the scripts of a sed command: each -e value, or else
// the first operand. It returns false for an option that may write or run.
func sedScriptIndices(args []string) ([]int, bool) {
	var scripts []int
	first := -1
	for i := 0; i < len(args); i++ {
		switch word := args[i]; {
		case word == "-e" || word == "--expression":
			i++
			scripts = append(scripts, i)
		case word == "--":
			if first < 0 && i+1 < len(args) {
				first = i + 1
			}
			i = len(args)
		case word == "-" || !strings.HasPrefix(word, "-"):
			if first < 0 {
				first = i
			}
		case !sedReadOption(word):
			return nil, false
		}
	}
	if len(scripts) == 0 && first >= 0 {
		scripts = []int{first}
	}
	return scripts, true
}

// sedReadOption reports whether a sed option only changes how sed reads and
// prints: -n, -E, -r, -s, -u and -z, alone or together.
func sedReadOption(word string) bool {
	return strings.Trim(word[1:], "nErsuz") == "" || slices.Contains([]string{"--quiet", "--silent", "--regexp-extended"}, word)
}

// plainAwkProgram returns the index of an awk program that cannot run, read
// or write anything by itself, with only -F and -v before it.
func plainAwkProgram(args []string) (int, bool) {
	for i := 0; i < len(args); i++ {
		word := args[i]
		switch {
		case word == "-F" || word == "-v":
			i++
		case word == "--":
			if i+1 < len(args) && !awkProgramUnsafe.MatchString(args[i+1]) {
				return i + 1, true
			}
			return 0, false
		case strings.HasPrefix(word, "-F") || strings.HasPrefix(word, "-v"):
		case strings.HasPrefix(word, "-"):
			return 0, false
		default:
			return i, !awkProgramUnsafe.MatchString(word)
		}
	}
	return 0, false
}

// verbatimWords returns the words of a part whose program runs nothing it is
// given, from the program on, and the files its redirects name. It returns
// false when the part needs the word-by-word reading instead: an open quote,
// a substitution, shell syntax outside quotes, or another program.
func verbatimWords(part string) ([]shellWord, []string, bool) {
	if strings.ContainsAny(part, "`") || strings.Contains(part, "$(") || strings.Contains(part, "<(") || strings.Contains(part, ">(") {
		return nil, nil, false
	}
	words, ok := shellWords(part)
	if !ok {
		return nil, nil, false
	}
	args, files := splitRedirects(words)
	for len(args) > 0 && execAssignment.MatchString(args[0].text) {
		args = args[1:]
	}
	if len(args) == 0 || slices.ContainsFunc(args, unquotedSyntax) {
		return nil, nil, false
	}
	return args, files, verbatimCommand(wordTexts(args))
}

// wordTexts returns the text of each word.
func wordTexts(words []shellWord) []string {
	texts := make([]string, len(words))
	for i, word := range words {
		texts[i] = word.text
	}
	return texts
}

// unquotedSyntax reports whether a word holds shell syntax outside quotes,
// such as a lone &, a subshell or a brace.
func unquotedSyntax(word shellWord) bool {
	for i := 0; i < len(word.text); i++ {
		if !word.quoted[i] && strings.IndexByte("&|;(){}", word.text[i]) >= 0 {
			return true
		}
	}
	return false
}

// verbatimOutside judges a part whose program runs nothing it is given, word
// by shell word. handled is false when the part needs the word-by-word
// reading instead. The words of a command in own are left to its parser. When
// a word may expand to an option (expandsWords), every word counts as a path.
func verbatimOutside(a Action, part string, own []string) (handled, outside bool) {
	words, files, ok := verbatimWords(part)
	if !ok {
		return false, false
	}
	patterns, named := searchPatterns(wordTexts(words))
	if verbatimFilesOutside(a, part, files, named) {
		return true, true
	}
	texts := wordTexts(words)
	if ownedCommand(texts, own) {
		return true, false
	}
	fields := withoutRedirects(strings.Fields(part))
	program, patterns := surePatterns(fields[max(execOperandStart(fields)-1, 0):], programName(words[0].text), patterns)
	options := true
	for i := 1; i < len(words); i++ {
		word := words[i]
		skip, next := skipsPatternValue(program, word.text)
		switch {
		case patterns[i]:
		case options && word.text == "--":
			options = false
		case options && skip:
			if next {
				i++
			}
		case (operandOutside(a, word.text, options) || operandOutside(a, word.raw, options)) &&
			(word.raw != word.text || !tempOperand(a, part, texts, i, word.text, options)):
			return true, true
		}
	}
	return true, false
}

// verbatimFilesOutside reports whether a file that a redirect of a verbatim
// part names, or that an option names in its own word (named), lies outside
// the work root. A file that an output redirect writes in the worker's own
// Claude session temp folder is the worker's own.
func verbatimFilesOutside(a Action, part string, files, named []string) bool {
	return slices.ContainsFunc(files, func(file string) bool {
		return outsideOperand(a, file) && (!redirectsInto(part, file) || !ownTemp(a, file, tempRedirect))
	}) || slices.ContainsFunc(named, func(file string) bool { return outsideOperand(a, file) })
}

// operandOutside reports whether a word names a file outside the work root.
// Before --, only the value of an option such as --file=x can name one.
func operandOutside(a Action, word string, options bool) bool {
	if options {
		word = pathWord(word)
	}
	return outsideOperand(a, word)
}

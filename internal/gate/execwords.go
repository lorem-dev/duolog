package gate

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/pathutil"
)

// shellWord is one word of a POSIX command as the program receives it.
type shellWord struct {
	text     string // quotes removed and escapes applied
	quoted   bool   // some of it was quoted or escaped
	redirect bool   // it starts with an unquoted redirect operator
}

// lexShell splits a POSIX command line into its simple commands, each with
// the words the shell passes on, and the operator after each: ";", "\n",
// "&&", "||", "|" or "" at the end. It returns false for what it does not
// expand itself, so that the caller can fall back to a coarser reading: a
// parameter, command or arithmetic substitution, a subshell, group or brace,
// a background &, a comment, a redirect inside a word, and an unbalanced quote.
func lexShell(command string) ([][]shellWord, []string, bool) {
	l := lexer{command: command}
	for l.i = 0; l.i < len(command); l.i++ {
		if !l.step() {
			return nil, nil, false
		}
	}
	l.cut("")
	return l.segments, l.ops, true
}

type lexer struct {
	command  string
	i        int
	text     strings.Builder
	inWord   bool
	word     shellWord
	words    []shellWord
	segments [][]shellWord
	ops      []string
}

func (l *lexer) end() {
	if l.inWord {
		l.word.text = l.text.String()
		l.words = append(l.words, l.word)
	}
	l.text.Reset()
	l.inWord, l.word = false, shellWord{}
}

func (l *lexer) cut(op string) {
	l.end()
	l.segments = append(l.segments, l.words)
	l.ops = append(l.ops, op)
	l.words = nil
}

func (l *lexer) next() byte {
	if l.i+1 < len(l.command) {
		return l.command[l.i+1]
	}
	return 0
}

func (l *lexer) add(s string, quoted bool) {
	l.text.WriteString(s)
	l.inWord = true
	l.word.quoted = l.word.quoted || quoted
}

func (l *lexer) step() bool {
	c := l.command[l.i]
	if l.separator(c) {
		return true
	}
	switch c {
	case '<', '>', '&', '|':
		return l.operator(c)
	case '\'':
		return l.singleQuoted()
	case '"':
		return l.doubleQuoted()
	case '\\':
		return l.escaped()
	}
	if strings.IndexByte("$`(){}", c) >= 0 || c == '#' && !l.inWord {
		return false
	}
	l.add(string(c), false)
	return true
}

// separator ends a word at whitespace, and a command at ;, a newline, |, ||
// and &&. A | right after > is the clobber redirect >|.
func (l *lexer) separator(c byte) bool {
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		l.end()
	case c == '\n' || c == ';':
		l.cut(string(c))
	case c == '|' && !strings.HasSuffix(l.text.String(), ">"), c == '&' && l.next() == '&':
		l.cut(l.pair(c))
	default:
		return false
	}
	return true
}

func (l *lexer) singleQuoted() bool {
	end := strings.IndexByte(l.command[l.i+1:], '\'')
	if end < 0 {
		return false
	}
	l.add(l.command[l.i+1:l.i+1+end], true)
	l.i += end + 1
	return true
}

// escaped keeps the character after a backslash, and drops a backslash and
// newline, which only continue the line.
func (l *lexer) escaped() bool {
	if l.i+1 >= len(l.command) {
		return false
	}
	l.i++
	if l.command[l.i] != '\n' {
		l.add(l.command[l.i:l.i+1], true)
	}
	return true
}

// pair returns the operator at c, doubled or single.
func (l *lexer) pair(c byte) string {
	if l.next() == c {
		l.i++
		return string([]byte{c, c})
	}
	return string(c)
}

// operator adds a redirect character. It may only start a word, after the
// digits of a stream number, or follow another one: & only next to < or >,
// and | only after >.
func (l *lexer) operator(c byte) bool {
	text := l.text.String()
	head := strings.TrimLeft(text, "0123456789")
	if strings.Trim(head, "<>&|") != "" || l.word.quoted || head == "" && text != "" && c != '<' && c != '>' {
		return false
	}
	if c == '&' && !strings.HasSuffix(head, "<") && !strings.HasSuffix(head, ">") && l.next() != '>' {
		return false
	}
	l.add(string(c), false)
	l.word.redirect = true
	return true
}

func (l *lexer) doubleQuoted() bool {
	var b strings.Builder
	for k := l.i + 1; k < len(l.command); k++ {
		d := l.command[k]
		switch {
		case d == '"':
			l.add(b.String(), true)
			l.i = k
			return true
		case d == '$' || d == '`':
			return false
		case d == '\\' && k+1 < len(l.command) && strings.IndexByte("$`\"\\\n", l.command[k+1]) >= 0:
			if l.command[k+1] != '\n' {
				b.WriteByte(l.command[k+1])
			}
			k++
		default:
			b.WriteByte(d)
		}
	}
	return false
}

var redirectOperators = []string{"&>>", "&>|", "&>", "<<<", "<<-", "<<", "<>", "<&", ">&", ">>", ">|", "<", ">"}

// splitRedirects takes the redirects out of a command's words and returns the
// files they name. A here-string's word is data, and a duplicate such as 2>&1
// names no file. A here-document is not read: its lines follow as commands.
func splitRedirects(words []shellWord) ([]shellWord, []string, bool) {
	var kept []shellWord
	var files []string
	for i := 0; i < len(words); i++ {
		if !words[i].redirect {
			kept = append(kept, words[i])
			continue
		}
		op, file := redirectOperator(words[i].text)
		if op == "" || op == "<<" || op == "<<-" {
			return nil, nil, false
		}
		if file == "" {
			if i+1 >= len(words) || words[i+1].redirect {
				return nil, nil, false
			}
			i++
			file = words[i].text
		}
		if namesFile(op, file) {
			files = append(files, file)
		}
	}
	return kept, files, true
}

// redirectOperator splits a redirect word into its operator, after any
// stream number, and the file written in the same word.
func redirectOperator(text string) (string, string) {
	text = strings.TrimLeft(text, "0123456789")
	for _, op := range redirectOperators {
		if strings.HasPrefix(text, op) {
			return op, strings.TrimPrefix(text, op)
		}
	}
	return "", ""
}

// namesFile reports whether a redirect's word is a file: not a here-string's
// data, nor the stream of a duplicate such as 2>&1 or >&-.
func namesFile(op, word string) bool {
	if op == "<<<" {
		return false
	}
	return op != "<&" && op != ">&" || word != "-" && !isNumber(word)
}

// lexedExecOutside is ExecOutside for a line that lexShell reads, run by a
// POSIX shell on a POSIX host: there a backslash escapes or is part of a name,
// never a path separator. ok is false when the line is not such a line.
func lexedExecOutside(a Action) (outside, ok bool) {
	if a.Work == "" || runtime.GOOS == "windows" || a.Shell == "powershell" || a.Shell == "windows" || pathutil.Windows(a.Work) || pathutil.Windows(a.Cwd) {
		return false, false
	}
	segments, ops, ok := lexShell(a.Target)
	if !ok {
		return false, false
	}
	cwd := a.Cwd
	if cwd == "" {
		cwd = a.Work
	}
	j := lexedJudge{a: a, dirs: []string{cwd}}
	for n, segment := range segments {
		words, files, ok := splitRedirects(segment)
		if !ok {
			return false, false
		}
		if slices.ContainsFunc(files, j.outside) {
			return true, true
		}
		words = afterKeywords(words)
		if j.wordsOutside(words) {
			return true, true
		}
		if !j.change(words, ops[n]) {
			return false, false
		}
	}
	return false, true
}

// afterKeywords drops the shell keywords in front of a command, which runs as
// it is after them.
func afterKeywords(words []shellWord) []shellWord {
	for len(words) > 1 && !words[0].quoted && slices.Contains([]string{"!", "do", "then", "else", "elif", "if", "while", "until"}, words[0].text) {
		words = words[1:]
	}
	return words
}

// lexedJudge judges the words of one line. dirs are the folders a relative
// path may start from: the hook's folder, and every folder a cd on the line
// may have moved to. Which one applies depends on what ran, so a path counts
// as outside when it is outside from any of them.
type lexedJudge struct {
	a    Action
	dirs []string
}

// change follows a cd or pushd to one named folder. A cd in a pipeline moves
// only its own subshell. It returns false for a move it cannot follow, such
// as cd - or popd.
func (j *lexedJudge) change(words []shellWord, op string) bool {
	if len(words) == 0 || words[0].text != "cd" && words[0].text != "pushd" {
		return len(words) == 0 || words[0].text != "popd"
	}
	if op == "|" {
		return true
	}
	target, ok := cdTarget(words[1:])
	if !ok || len(j.dirs) > 8 {
		return false
	}
	for _, dir := range j.dirs {
		next := target
		if !filepath.IsAbs(next) {
			next = filepath.Join(dir, next)
		}
		if !slices.Contains(j.dirs, next) {
			j.dirs = append(j.dirs, next)
		}
	}
	return true
}

// cdTarget returns the folder cd moves to: its one operand, or the home
// folder. cd - goes back to a folder the line does not name.
func cdTarget(args []shellWord) (string, bool) {
	var operands []string
	for _, word := range args {
		if !strings.HasPrefix(word.text, "-") || word.text == "-" {
			operands = append(operands, word.text)
		}
	}
	target := "~"
	if len(operands) == 1 {
		target = operands[0]
	}
	if len(operands) > 1 || target == "-" {
		return "", false
	}
	return homePath(target)
}

// homePath expands ~ and ~/ to the home folder. ~user is another user's home,
// which it cannot place.
func homePath(target string) (string, bool) {
	if target != "~" && !strings.HasPrefix(target, "~/") {
		return target, !strings.HasPrefix(target, "~")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, strings.TrimPrefix(target, "~")), true
}

// wordsOutside judges the words of one command after its redirects. The
// operands are judged as files, except the pattern or script that
// patternOperand names and the values of patternOptions. A quoted word with
// whitespace in it, wherever it stands, may be a script for another program,
// so each of its pieces is judged too, as a coarse reading would.
func (j *lexedJudge) wordsOutside(words []shellWord) bool {
	texts := wordTexts(words)
	start, patterns, valued := operandRoles(texts)
	options := true
	for i, text := range texts {
		if !patterns[i] && strings.ContainsAny(text, " \t\n") && j.piecesOutside(text) {
			return true
		}
		if i < start || patterns[i] {
			continue
		}
		switch {
		case !options:
			if j.outside(text) {
				return true
			}
		case text == "--":
			options = false
		case slices.Contains(valued, strings.SplitN(text, "=", 2)[0]):
			// The value is a pattern too, in the word or the next one.
			patterns[i+1] = patterns[i+1] || !strings.Contains(text, "=")
		case j.outside(pathWord(text)):
			return true
		}
	}
	return false
}

func wordTexts(words []shellWord) []string {
	texts := make([]string, len(words))
	for i, word := range words {
		texts[i] = word.text
	}
	return texts
}

// operandRoles returns where a command's own words start, which of them are
// patterns, and the program's options whose value is a pattern.
func operandRoles(texts []string) (int, map[int]bool, []string) {
	start := execOperandStart(texts)
	patterns := map[int]bool{patternOperand(texts, start): true}
	var valued []string
	if start > 0 && start <= len(texts) {
		valued = patternOptions[programName(texts[start-1])]
	}
	return start, patterns, valued
}

func (j *lexedJudge) piecesOutside(text string) bool {
	return slices.ContainsFunc(strings.Fields(text), func(piece string) bool {
		return j.outside(pathWord(strings.Trim(piece, "'\"")))
	})
}

// outside reports whether a word that names a file names one outside the
// work root from any of the folders, other than a stream device or a file in
// the worker's own temp folder.
func (j *lexedJudge) outside(target string) bool {
	if target == "" {
		return false
	}
	clean := path.Clean(target)
	if slices.Contains([]string{"/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr"}, clean) || strings.HasPrefix(clean, "/dev/fd/") {
		return false
	}
	target, ok := homePath(target)
	if !ok || windowsDrive.MatchString(target) {
		return true
	}
	work, ok := resolvePOSIX(j.a.Work)
	if !ok {
		return true
	}
	for _, dir := range j.dirs {
		full := target
		if !filepath.IsAbs(full) {
			// Not Join, which would clean away a .. that InTemp refuses.
			full = dir + string(filepath.Separator) + full
		}
		if InTemp(j.a.Temp, full) {
			continue
		}
		resolved, ok := resolvePOSIX(full)
		if !ok || !within(work, resolved) {
			return true
		}
	}
	return false
}

// sedPrint matches a sed script that only prints: one address or a range of
// line numbers, $ or /regex/, then p.
var sedPrint = regexp.MustCompile(`^(?:(?:[0-9]+|\$|/(?:[^/\\\n]|\\.)*/)(?:,(?:[0-9]+|\$|/(?:[^/\\\n]|\\.)*/))?)?p$`)

// awkFiles lists what lets an awk program name a file: an output redirect, a
// pipe, getline (the only reader of < file), system and the argument list.
var awkFiles = []string{">", "|", "getline", "system", "ARGV", "ARGC", "@include", "@load"}

// patternOperand returns the index of the operand that a program takes as a
// pattern or script rather than a file, or -1: the first operand of grep, rg
// and git grep without -e, -f or --files; a sed script that only prints; and
// an awk program that names no file. start is where the program's own words
// begin.
func patternOperand(words []string, start int) int {
	if start <= 0 || start > len(words) {
		return -1
	}
	args := words[start:]
	switch name := programName(words[start-1]); name {
	case "git":
		if len(args) > 0 && args[0] == "grep" {
			return searchPattern("grep", args[1:], start+1)
		}
	case "grep", "egrep", "fgrep", "rg":
		return searchPattern(name, args, start)
	case "sed":
		if i := firstOperand(args, "ef", []string{"-l", "--line-length"}); i >= 0 && sedPrint.MatchString(args[i]) {
			return start + i
		}
	case "awk", "gawk", "mawk", "nawk":
		i := firstOperand(args, "f", []string{"-v", "-F", "--assign", "--field-separator"})
		if i >= 0 && !slices.ContainsFunc(awkFiles, func(s string) bool { return strings.Contains(args[i], s) }) {
			return start + i
		}
	}
	return -1
}

// searchPattern returns the index of grep's or rg's pattern operand, or -1
// when -e, -f or rg --files means there is none.
func searchPattern(name string, args []string, start int) int {
	if name != "rg" {
		name = "grep"
	}
	if explicitPattern(args) || name == "rg" && slices.Contains(args, "--files") {
		return -1
	}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--":
			if i+1 < len(args) {
				return start + i + 1
			}
			return -1
		case strings.HasPrefix(args[i], "-") && args[i] != "-":
			if separateValue(name, args[i]) {
				i++
			}
		default:
			return start + i
		}
	}
	return -1
}

// firstOperand returns the index of the first operand of sed or awk, or -1
// when an option gives the script instead: a short option with one of
// letters in it, such as sed -e or awk -f, or a long one such as --file.
// values are the options whose value is the next word.
func firstOperand(args []string, letters string, values []string) int {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			if i+1 < len(args) {
				return i + 1
			}
			return -1
		case arg == "-" || !strings.HasPrefix(arg, "-"):
			return i
		case scriptOption(arg, letters):
			return -1
		case slices.Contains(values, arg):
			i++
		}
	}
	return -1
}

func scriptOption(arg, letters string) bool {
	if strings.HasPrefix(arg, "--") {
		name, _, _ := strings.Cut(arg, "=")
		return slices.Contains([]string{"--expression", "--file", "--source", "--include"}, name)
	}
	return strings.ContainsAny(arg[1:], letters)
}

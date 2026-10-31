package peer

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// State is what a peer session is doing, read from a bounded screen sample.
type State string

const (
	Starting State = "starting"
	Updating State = "updating"
	Question State = "question"
	Ready    State = "ready"
	Limited  State = "limited"
	Exited   State = "exited"
	Closed   State = "closed"
)

// Option is one choice of a question. Number is zero for an unnumbered list.
type Option struct {
	Number   int
	Label    string
	Selected bool
}

// Screen is the reading of one sample. It holds only what duolog acts on, and
// the text of a question; everything else on the screen is dropped.
type Screen struct {
	State    State
	Question string
	Options  []Option
	// Working is true while the agent shows that it can be interrupted.
	Working bool
	// Composer is true when an input line is visible; Input is its text and
	// Empty is true when it is blank or shows only the agent's placeholder.
	Composer bool
	Input    string
	Empty    bool
}

const (
	markers          = "›❯"
	maxQuestionBytes = 200
	maxHeadBytes     = 120
)

var (
	workingRe  = regexp.MustCompile(`(?i)\besc to interrupt\b|\bctrl\+c to interrupt\b`)
	updatingRe = regexp.MustCompile(`(?i)==> (?:upgrading|installing|downloading|fetching|pouring)|updating (?:codex|claude)|brew upgrade|installing update|downloading update`)
	limitRe    = regexp.MustCompile(`(?i)you.ve hit your [^\n]*limit`)
	yesNoRe    = regexp.MustCompile(`(?i)\((?:y/n|yes/no)\)|\[(?:y/n|yes/no)\]`)
	hintRe     = regexp.MustCompile(`(?i)\benter\b.*\besc(?:ape)?\b`)
	numberedRe = regexp.MustCompile(`^\s*(?:([` + markers + `])\s*)?(\d+)\.\s+(\S.*?)\s*$`)
	cursorRe   = regexp.MustCompile(`^(\s*)([` + markers + `])\s+(\S.*?)\s*$`)
	ruleRe     = regexp.MustCompile(`^\s*[─╌━]{5,}\s*$`)
	claudeTry  = regexp.MustCompile(`^Try ".*"$`)
	gapRe      = regexp.MustCompile(`\s{2,}`)
)

// Classify reads a captured screen for the named agent. The text of a question
// is returned for a question and for nothing else.
func Classify(agent string, screen []byte) (State, string) {
	s := Parse(agent, screen)
	return s.State, s.Question
}

// Parse never treats screen text as an instruction: it only recognizes fixed
// shapes, and an unrecognized screen is Starting.
func Parse(agent string, raw []byte) Screen {
	lines := screenLines(raw)
	s := Screen{State: Starting}
	for _, line := range lines {
		if workingRe.MatchString(line) {
			s.Working = true
		}
	}
	if options, first, ok := findQuestion(lines); ok {
		s.State, s.Options = Question, options
		s.Question = questionText(lines, first, options)
		return s
	}
	s.Composer, s.Input, s.Empty = findComposer(agent, lines)
	switch {
	case updatingRe.MatchString(strings.Join(lines, "\n")):
		s.State = Updating
	case s.Working || !s.Composer:
	case limitRe.MatchString(strings.Join(lines, "\n")):
		s.State = Limited
	default:
		s.State = Ready
	}
	return s
}

func screenLines(raw []byte) []string {
	text := strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r == '\n':
			return r
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, string(raw))
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}
	return lines
}

// findQuestion looks for a selection list or a yes or no prompt. It returns the
// options and the index of the first line that belongs to them.
func findQuestion(lines []string) ([]Option, int, bool) {
	cursor := -1
	for i, line := range lines {
		if m := numberedRe.FindStringSubmatch(line); m != nil && m[1] != "" {
			cursor = i
		}
	}
	if cursor >= 0 {
		options, first := numberedOptions(lines, cursor)
		return options, first, true
	}
	for i, line := range lines {
		if yesNoRe.MatchString(line) {
			return nil, i, true
		}
	}
	hint := false
	for _, line := range lines {
		if hintRe.MatchString(line) {
			hint = true
		}
	}
	if !hint {
		return nil, 0, false
	}
	for i, line := range lines {
		at := cursorRe.FindStringSubmatchIndex(line)
		// A composer sits under a rule; it is not a menu even when it holds text.
		if at == nil || aboveIsRule(lines, i) {
			continue
		}
		return alignedOptions(lines, i, utf8.RuneCountInString(line[:at[6]])), i, true
	}
	return nil, 0, false
}

func aboveIsRule(lines []string, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) != "" {
			return ruleRe.MatchString(lines[j])
		}
	}
	return false
}

// numberedOptions takes the run of consecutive numbers that holds the cursor.
func numberedOptions(lines []string, cursor int) ([]Option, int) {
	var run []Option
	var rows []int
	var best []Option
	var bestRows []int
	flush := func() {
		for _, row := range rows {
			if row == cursor {
				best, bestRows = run, rows
			}
		}
		run, rows = nil, nil
	}
	for i, line := range lines {
		m := numberedRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if len(run) > 0 && n != run[len(run)-1].Number+1 {
			flush()
		}
		run = append(run, Option{Number: n, Label: label(m[3]), Selected: m[1] != ""})
		rows = append(rows, i)
	}
	flush()
	return best, bestRows[0]
}

// alignedOptions reads an unnumbered list from the cursor row down: the lines
// that start in the same column as its label.
func alignedOptions(lines []string, cursor, col int) []Option {
	m := cursorRe.FindStringSubmatch(lines[cursor])
	options := []Option{{Label: label(m[3]), Selected: true}}
	for _, line := range lines[cursor+1:] {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || len(line)-len(trimmed) != col || ruleRe.MatchString(line) {
			break
		}
		options = append(options, Option{Label: label(trimmed)})
	}
	return options
}

// label drops the description a menu prints after its label.
func label(text string) string {
	return strings.TrimSpace(gapRe.Split(text, 2)[0])
}

func questionText(lines []string, first int, options []Option) string {
	var before []string
	for _, line := range lines[:first] {
		if t := strings.Join(strings.Fields(line), " "); t != "" && !ruleRe.MatchString(line) {
			before = append(before, t)
		}
	}
	if len(options) == 0 {
		// A yes or no prompt: the line itself is the question.
		before = []string{strings.Join(strings.Fields(lines[first]), " ")}
	}
	head := strings.Join(before[:min(3, len(before))], " ")
	ask := ""
	if i := strings.Index(head, "?"); i >= 0 {
		head = head[:i+1]
	} else {
		for _, line := range before[min(3, len(before)):] {
			// A line that starts in lower case continues a wrapped sentence.
			if i := strings.Index(line, "?"); i >= 0 && !unicode.IsLower([]rune(line)[0]) {
				ask = line[:i+1]
			}
		}
	}
	if ask != "" {
		head = cut(head, maxHeadBytes-len(ask)-1) + " " + ask
	}
	text := cut(head, maxHeadBytes)
	if len(options) > 0 {
		var list []string
		for _, o := range options {
			item := o.Label
			if o.Number > 0 {
				item = strconv.Itoa(o.Number) + ". " + item
			}
			list = append(list, item)
		}
		text += " [" + strings.Join(list, " | ") + "]"
	}
	return cut(text, maxQuestionBytes)
}

// cut keeps whole runes.
func cut(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// findComposer returns the agent's input line. Codex marks it with ›. Claude
// marks it with ❯ between two rules, which keeps it apart from a menu.
func findComposer(agent string, lines []string) (found bool, input string, empty bool) {
	marker := ""
	switch agent {
	case "codex":
		marker = "›"
	case "claude":
		marker = "❯"
	default:
		return false, "", false
	}
	for i := len(lines) - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(strings.TrimLeft(lines[i], " "), marker)
		if !ok {
			continue
		}
		if agent == "claude" && !aboveIsRule(lines, i) {
			continue
		}
		input = strings.TrimSpace(rest)
		if input == "Ask Codex to do anything" || claudeTry.MatchString(input) {
			input = ""
		}
		return true, input, input == ""
	}
	return false, "", false
}

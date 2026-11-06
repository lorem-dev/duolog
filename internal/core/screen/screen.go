// Package screen reads the bounded sample of a peer pane and decides on it. It
// holds no I/O: the peer package captures the screen and acts on the reading.
package screen

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
	// Interrupted is an idle input line under the agent's interrupt notice,
	// such as Claude's "Interrupted · What should Claude do instead?" after a
	// denied permission prompt. The agent waits for a follow-up.
	Interrupted State = "interrupted"
	Limited     State = "limited"
	Exited      State = "exited"
	Closed      State = "closed"
	// Gone is a pane that Rex no longer knows, as after a Rex restart. Only
	// List reports it; its record stays until peer close frees it.
	Gone State = "gone"
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
	// Working is true while the agent shows that it is at work: an interrupt
	// hint, its working line, a running hook or a background terminal.
	Working bool
	// Queued means Codex has a question behind its follow-up input panel.
	Queued bool
	// Composer is true when an input line is visible; Input is its text and
	// Empty is true when it is blank or shows only the agent's placeholder or
	// suggestion: a known shape, or text drawn dim or grey in a styled capture.
	Composer bool
	Input    string
	Empty    bool
	// Rows are the input's rows when a long line wraps, Input first. Claude's
	// rows end at the rule below the box; Codex has none, so its rows run to the
	// end of the screen and may hold its footer.
	Rows []string
	// Text is the cleaned screen, kept to read a limit message.
	Text string
}

// CommandQuestion is the fixed prompt Codex shows above a command it wants to run.
const CommandQuestion = "Would you like to run the following command?"

const (
	markers          = "›❯"
	maxQuestionBytes = 200
	maxHeadBytes     = 120
)

// workingRe: the working line may start with any spinner or blink frame and be
// cut at the pane's edge. Hook and terminal lines start the line, so the same
// words in the agent's own text do not count.
var (
	queuedCountRe = regexp.MustCompile(`\?\s+[1-9][0-9]*\s+questions?\b`)
	workingRe     = regexp.MustCompile(`(?i)\besc to interrupt\b|\bctrl\+c to interrupt\b|^\s*\W?\s*Working\s*\(|^\W*running (?:\S+ )?hooks?\b|\b\d+ background terminals? running\b|^\W*waiting for background terminals?\b`)
	updatingRe    = regexp.MustCompile(`(?i)==> (?:upgrading|installing|downloading|fetching|pouring)|updating (?:codex|claude)|brew upgrade|installing update|downloading update`)
	limitRe       = regexp.MustCompile(`(?i)you.ve hit your [^\n]*limit`)
	yesNoRe       = regexp.MustCompile(`(?i)\((?:y/n|yes/no)\)|\[(?:y/n|yes/no)\]`)
	hintRe        = regexp.MustCompile(`(?i)\benter\b.*\besc(?:ape)?\b`)
	numberedRe    = regexp.MustCompile(`^\s*(?:([` + markers + `])\s*)?(\d+)\.\s+(\S.*?)\s*$`)
	cursorRe      = regexp.MustCompile(`^(\s*)([` + markers + `])\s+(\S.*?)\s*$`)
	ruleRe        = regexp.MustCompile(`^\s*[─╌━]{5,}\s*$`)
	// claudeTry is Claude's placeholder. A long one wraps or is cut at the
	// pane's edge, so only its start is matched.
	claudeTry = regexp.MustCompile(`^Try[\s\x{00a0}]+["“]`)
	gapRe     = regexp.MustCompile(`\s{2,}`)
	// The notices an agent prints above its input line when a turn was
	// interrupted, by Esc or by a denied permission prompt.
	interruptedRe = map[string]*regexp.Regexp{
		"claude": regexp.MustCompile(`^[\s⎿│]*Interrupted(?:\s*·\s*What should Claude do instead\?|\s+by user\b)`),
		"codex":  regexp.MustCompile(`^[\s■•]*Conversation interrupted\b`),
	}
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
	lines, solid := screenLines(raw)
	s := Screen{State: Starting, Text: strings.Join(lines, "\n")}
	for _, line := range lines {
		if workingRe.MatchString(line) {
			s.Working = true
		}
	}
	if options, first, ok := findQuestion(lines); ok {
		s.State, s.Options = Question, options
		s.Question = questionText(lines, first, options)
		if agent == "codex" && strings.Contains(s.Text, CommandQuestion) {
			if command := PromptCommand(s.Text); command != "" {
				menu := ""
				if i := strings.Index(s.Question, "["); i >= 0 {
					menu = " " + s.Question[i:]
				}
				s.Question = CommandQuestion + " " + oneLine(command) + menu
			}
		}
		return s
	}
	if agent == "codex" && (strings.Contains(s.Text, "Queued follow-up inputs") || queuedCountRe.MatchString(s.Text)) && strings.Contains(s.Text, "shift+left to answer") {
		s.Queued = true
		s.State = Question
		s.Question = Cut(oneLine(s.Text), maxQuestionBytes)
		return s
	}
	at, input, empty, rows := findComposer(agent, lines, solid)
	s.Composer, s.Input, s.Empty, s.Rows = at >= 0, input, empty, rows
	switch {
	case updatingRe.MatchString(s.Text):
		s.State = Updating
	case s.Working || !s.Composer:
	case limitRe.MatchString(s.Text):
		s.State = Limited
	case interrupted(agent, lines, at):
		s.State = Interrupted
	default:
		s.State = Ready
	}
	return s
}

// interrupted reports the agent's interrupt notice as the last line above its
// input line: above Claude's box, or above Codex's marker. A follow-up that
// the agent answered puts its own lines below the notice, so an old notice
// does not count.
func interrupted(agent string, lines []string, at int) bool {
	re := interruptedRe[agent]
	if re == nil || at < 0 {
		return false
	}
	above := at
	if agent == "claude" {
		// The rule above the box; findComposer made sure it is there.
		for above--; above >= 0 && !ruleRe.MatchString(lines[above]); above-- {
		}
	}
	for j := above - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) != "" {
			return re.MatchString(lines[j])
		}
	}
	return false
}

// screenLines returns the plain lines of a capture and, rune for rune, its
// solid lines: the same text with every rune drawn dim or grey made a space.
// SGR sequences set the styles, and each line starts unstyled, so a style
// that a dropped line set is never carried into the next. Other control
// characters are dropped. A plain capture has solid lines equal to its lines.
func screenLines(raw []byte) (lines, solid []string) {
	for _, line := range strings.Split(string(raw), "\n") {
		plain, typed := styledLine(line)
		lines = append(lines, strings.TrimRightFunc(plain, unicode.IsSpace))
		solid = append(solid, strings.TrimRightFunc(typed, unicode.IsSpace))
	}
	return lines, solid
}

var sgrRe = regexp.MustCompile("^\x1b\\[([0-9;:]*)m")

type cell struct {
	r              rune
	faint, inverse bool
}

func styledLine(line string) (string, string) {
	var cells []cell
	var st sgrState
	for i := 0; i < len(line); {
		if m := sgrRe.FindStringSubmatch(line[i:]); m != nil {
			st.apply(m[1])
			i += len(m[0])
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		i += size
		switch {
		case r == '\t':
			r = ' '
		case unicode.IsControl(r):
			continue
		}
		cells = append(cells, cell{r: r, faint: st.dim || st.grey, inverse: st.inverse})
	}
	// The cursor is an inverse cell. On a suggestion it sits on the first
	// cell, so it takes the look of the cell after it.
	for i := range cells {
		if cells[i].inverse && i+1 < len(cells) && !cells[i+1].inverse {
			cells[i].faint = cells[i+1].faint
		}
	}
	var plain, typed strings.Builder
	for _, c := range cells {
		plain.WriteRune(c.r)
		if c.faint {
			typed.WriteRune(' ')
		} else {
			typed.WriteRune(c.r)
		}
	}
	return plain.String(), typed.String()
}

// sgrState is the part of the SGR state that tells a suggestion apart: dim,
// a grey foreground and inverse video, which draws the cursor.
type sgrState struct{ dim, grey, inverse bool }

func (s *sgrState) apply(params string) {
	if params == "" {
		*s = sgrState{}
		return
	}
	p := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	num := func(i int) int {
		if i >= len(p) {
			return -1
		}
		n, err := strconv.Atoi(p[i])
		if err != nil {
			return -1
		}
		return n
	}
	for i := 0; i < len(p); i++ {
		switch n := num(i); {
		case n == 0:
			*s = sgrState{}
		case n == 2:
			s.dim = true
		case n == 22:
			s.dim = false
		case n == 7:
			s.inverse = true
		case n == 27:
			s.inverse = false
		case n == 90:
			s.grey = true
		case n >= 30 && n <= 37 || n == 39 || n >= 91 && n <= 97:
			s.grey = false
		case n == 38 || n == 48:
			// An extended colour: 5;index or 2;r;g;b. Only the foreground counts.
			switch num(i + 1) {
			case 5:
				if n == 38 {
					s.grey = greyIndex(num(i + 2))
				}
				i += 2
			case 2:
				if n == 38 {
					s.grey = greyRGB(num(i+2), num(i+3), num(i+4))
				}
				i += 4
			}
		}
	}
}

// greyIndex is the bright black of the 16 colours or a middle step of the
// 256-colour grey ramp; the ramp's ends are near black and near white.
func greyIndex(n int) bool { return n == 8 || n >= 238 && n <= 250 }

// greyRGB is a colour with equal parts that is neither dark nor light, such
// as the rgb(153,153,153) that Claude draws its suggestions in.
func greyRGB(r, g, b int) bool {
	if r < 0 || g < 0 || b < 0 {
		return false
	}
	hi, lo := max(r, g, b), min(r, g, b)
	return hi-lo <= 16 && lo >= 64 && hi <= 192
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

// IsRule reports a line that is one horizontal rule.
func IsRule(line string) bool { return ruleRe.MatchString(line) }

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
			if t == "Queued follow-up inputs" || queuedCountRe.MatchString(t) || strings.Contains(t, "shift+left to answer") {
				continue
			}
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
		head = Cut(head, maxHeadBytes-len(ask)-1) + " " + ask
	}
	text := Cut(head, maxHeadBytes)
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
	return Cut(text, maxQuestionBytes)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// PromptCommand reads the displayed command, not the bounded question summary.
// Only the command block below the fixed prompt and before the menu counts.
func PromptCommand(text string) string {
	_, rest, ok := strings.Cut(text, CommandQuestion)
	if !ok {
		return ""
	}
	var command []string
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if numberedRe.MatchString(line) {
			break
		}
		if len(command) == 0 {
			if c, ok := strings.CutPrefix(line, "$ "); ok {
				command = append(command, c)
			}
		} else {
			if line == "" {
				break
			}
			command = append(command, line)
		}
	}
	return strings.Join(command, "\n")
}

// Cut keeps whole runes.
func Cut(s string, n int) string {
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

// findComposer returns the row of the agent's input line, or -1. Codex marks
// it with ›. Claude marks it with ❯ between two rules, which keeps it apart
// from a menu. The input is read from the solid lines, so a dim suggestion
// is no input; a placeholder of a known shape is none either, along with the
// rows it wraps over.
func findComposer(agent string, lines, solid []string) (at int, input string, empty bool, rows []string) {
	var marker string
	switch agent {
	case "codex":
		marker = "›"
	case "claude":
		marker = "❯"
	default:
		return -1, "", false, nil
	}
	for i := len(lines) - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(strings.TrimLeft(lines[i], " "), marker)
		if !ok {
			continue
		}
		if agent == "claude" && !aboveIsRule(lines, i) {
			continue
		}
		shown := strings.TrimSpace(rest)
		if shown == "Ask Codex to do anything" || claudeTry.MatchString(shown) {
			return i, "", true, []string{""}
		}
		// The solid line has the same runes in the same places, so the input
		// starts after as many runes there.
		skip := utf8.RuneCountInString(lines[i]) - utf8.RuneCountInString(rest)
		input = strings.TrimSpace(string([]rune(solid[i])[min(skip, utf8.RuneCountInString(solid[i])):]))
		rows = []string{input}
		for j := i + 1; j < len(lines); j++ {
			if agent == "claude" && ruleRe.MatchString(lines[j]) {
				break
			}
			rows = append(rows, strings.TrimSpace(solid[j]))
		}
		return i, input, input == "", rows
	}
	return -1, "", false, nil
}

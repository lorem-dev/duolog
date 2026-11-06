package peer

import "strings"

// The screens that the tests of this package give the fake pane. The tests of
// the screen reading itself, in core/screen, keep their own copy.

func codexIdle(text string) string {
	if text == "" {
		text = "Ask Codex to do anything"
	}
	return "\n\n› " + text + "\n\n  GPT-6-Luna low · /tmp/x\n  ← for agents · ? for shortcuts\n"
}

func claudeIdle(text string) string {
	if text == "" {
		text = `Try "how does <filepath> work?"`
	}
	rule := strings.Repeat("─", 64)
	return "\n" + rule + "\n❯ " + text + "\n" + rule + "\n  ⏸ manual mode on · ? for shortcuts\n"
}

// claudeWrapped is a Claude input box in a pane of the given width. Rows break
// at the edge, inside a word as well, and the rows after the first are indented.
func claudeWrapped(width int, text string) string {
	rule := strings.Repeat("─", width)
	rows := ""
	for prefix, rest := "❯ ", []rune(text); len(rest) > 0; prefix = "  " {
		n := min(width-2, len(rest))
		rows += prefix + string(rest[:n]) + "\n"
		rest = rest[n:]
	}
	return "\n" + rule + "\n" + rows + rule + "\n  ⏸ manual mode on · ? for shortcuts\n"
}

func claudeWorking() string {
	return "✻ Channelling… (3s · esc to interrupt)\n" + claudeIdle("")
}

// workingScreens show an agent at work without the full interrupt hint: the
// status line in its other blink frame and cut at the pane's edge, a running
// hook, and a background terminal.
var workingScreens = []struct{ agent, line string }{
	{"codex", "◦ Working (1m 02s • esc to inte…"},
	{"codex", "• Running PreToolUse hook"},
	{"codex", "• Running PostToolUse hook: duolog gate"},
	{"claude", "  ⎿  Running PostToolUse hooks… (1/2 done)"},
	{"codex", "  1 background terminal running · /ps to view · /stop to close"},
	{"codex", "• Waiting for background terminal"},
}

// claudeInterrupted is the recorded pane after the lead denied a permission
// prompt: the notice is the last line above the input box.
func claudeInterrupted() string { return fixtureText("claude-interrupted.txt") }

// styledIdle is the recorded ready pane with its input row as a styled
// capture gives it. The recorded row has a no-break space after the marker.
func styledIdle(row string) string {
	ready := fixtureText("claude-ready.txt")
	placeholder := `Try "how does <filepath> work?"`
	if !strings.Contains(ready, "❯ "+placeholder) {
		panic("claude-ready.txt has no placeholder row")
	}
	return strings.Replace(ready, placeholder, row, 1)
}

const (
	grey   = "\x1b[38;2;153;153;153m"
	cursor = "\x1b[7m \x1b[27m"
)

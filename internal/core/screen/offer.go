package screen

import (
	"regexp"
	"strings"
)

// Offer is an agent's own question whether to install a plugin, read from a
// screen by the agent's pane. Plugin is empty for a question without its
// metadata.
type Offer struct {
	Plugin, Marketplace, Trigger string
	// By names who offered, as the lead's note says it ("Claude").
	By string
	// Install is the command that installs the plugin, for the lead's note;
	// empty when the offer names no plugin.
	Install string
}

var installAskRe = regexp.MustCompile(`(?i)\binstall\b.*\bplugins?\b.*\?$`)

// InstallAsk reports a line that asks whether to install a plugin.
func InstallAsk(line string) bool { return installAskRe.MatchString(line) }

// InstallQuestion recognizes a question that asks to install a plugin, whoever
// asks it, without its metadata, which may be missing, wrapped or above the
// capture. A question line wrapped onto the next line is read joined. An
// install question never reaches the text rules of auto_answers.
func InstallQuestion(s Screen) bool {
	if s.State != Question || len(s.Options) == 0 {
		return false
	}
	previous := ""
	for _, line := range strings.Split(s.Text, "\n") {
		line = strings.TrimSpace(line)
		if IsRule(line) {
			previous = ""
			continue
		}
		if InstallAsk(line) || previous != "" && InstallAsk(previous+" "+line) {
			return true
		}
		previous = line
	}
	return false
}

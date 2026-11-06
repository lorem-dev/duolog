package screen

import "github.com/lorem-dev/duolog/internal/core/agent"

// The adapters own these values (adapter/claude and adapter/codex Facts.Pane);
// core cannot import them, so the tests carry copies. Each adapter's TestFacts
// pins the same values.
var (
	claudePane = agent.PaneFacts{
		Placeholder: `^Try[\s\x{00a0}]+["“]`,
		Updater:     []string{"updating claude"},
		Interrupt:   []string{`^[\s⎿│]*Interrupted(?:\s*·\s*What should Claude do instead\?|\s+by user\b)`},
		Composer:    agent.ComposerFacts{Marker: "❯", Boxed: true},
	}
	codexPane = agent.PaneFacts{
		Placeholder:     `^Ask Codex to do anything$`,
		Updater:         []string{"updating codex"},
		Interrupt:       []string{`^[\s■•]*Conversation interrupted\b`},
		Composer:        agent.ComposerFacts{Marker: "›"},
		Answers:         []agent.AutoAnswer{{Verdict: "allow", Label: "Yes, proceed"}, {Verdict: "deny", Label: "No"}},
		CommandQuestion: "Would you like to run the following command?",
		Queued:          "shift+left to answer",
	}
)

// factsOf gives the pane facts of the named agent; an unknown agent has none.
func factsOf(name string) agent.PaneFacts {
	switch name {
	case "claude":
		return claudePane
	case "codex":
		return codexPane
	}
	return agent.PaneFacts{}
}

func parseAs(name string, raw []byte) Screen { return Parse(raw, factsOf(name)) }

func classifyAs(name string, raw []byte) (State, string) { return Classify(raw, factsOf(name)) }

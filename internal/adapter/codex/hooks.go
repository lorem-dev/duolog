package codex

import (
	"encoding/json"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// Events renders Codex's hook output and reads its permission mode.
type Events struct{}

// Show adds text to out as systemMessage, which Codex shows the person as a
// warning on SessionStart, UserPromptSubmit and Stop. A Stop needs JSON on
// stdout, which this gives when out is empty. Codex hooks have no terminal
// sequence, so alert adds nothing: Codex's own tui.notifications alerts the
// person when its turn ends.
func (Events) Show(out []byte, text string, _ bool) []byte {
	return kit.AddFields(out, map[string]string{"systemMessage": text})
}

// Render wraps text in the hookSpecificOutput envelope that Codex reads from a
// synchronous hook. asyncRewake instead requires exit 2 and plain stderr, so
// it does not come through here.
func (Events) Render(event string, text []byte) []byte {
	if len(text) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": string(text)}})
	return append(encoded, '\n')
}

// Mode reads the mode of the last turn context in the transcript; Codex's
// hook payload does not say it.
func (Events) Mode(_ []byte, transcript string) (mode.Mode, bool) {
	state, _ := session.ReadLeadState(Facts.Name, transcript)
	return state.Mode, state.Mode != ""
}

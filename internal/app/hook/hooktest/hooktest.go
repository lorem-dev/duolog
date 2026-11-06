// Package hooktest holds the fake Events of the hook tests and the contract
// that every Events must meet.
package hooktest

import (
	"encoding/json"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/hook"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// Events is the port under test, named here so that an adapter's test need not
// import the app package.
type Events = hook.Events

// RenderCall is one call of Render.
type RenderCall struct {
	Event string
	Text  []byte
}

// ModeCall is one call of Mode.
type ModeCall struct {
	Payload    []byte
	Transcript string
}

// Fake is an Events that records its calls and returns scripted results. Its
// Render writes the envelope every agent writes today; its Mode returns the
// scripted mode.
type Fake struct {
	Observed    mode.Mode // the mode Mode returns
	HasMode     bool      // whether Mode says one
	RenderCalls []RenderCall
	ModeCalls   []ModeCall
}

var _ Events = (*Fake)(nil)

// Render records the call and returns the envelope, or nothing for no text.
func (f *Fake) Render(event string, text []byte) []byte {
	f.RenderCalls = append(f.RenderCalls, RenderCall{Event: event, Text: text})
	if len(text) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": string(text)}})
	return append(encoded, '\n')
}

// Mode records the call and returns the scripted answer.
func (f *Fake) Mode(payload []byte, transcript string) (mode.Mode, bool) {
	f.ModeCalls = append(f.ModeCalls, ModeCall{Payload: payload, Transcript: transcript})
	return f.Observed, f.HasMode
}

// EventsContract runs the cases that every Events meets: empty text renders
// nothing, and text renders the hookSpecificOutput envelope byte for byte.
func EventsContract(t *testing.T, newEvents func(t *testing.T) Events) {
	t.Helper()
	t.Run("empty text renders nothing", func(t *testing.T) {
		e := newEvents(t)
		for _, text := range [][]byte{nil, {}} {
			if got := e.Render("SessionStart", text); len(got) != 0 {
				t.Errorf("Render of empty text = %q; want nothing", got)
			}
		}
	})
	t.Run("text renders the envelope", func(t *testing.T) {
		e := newEvents(t)
		for event, text := range map[string]string{
			"SessionStart":     "duolog: 1 message\n\"quoted\" <tag> & more\n",
			"UserPromptSubmit": "one line\n",
			"PostToolUse":      "tab\tand unicode é\n",
		} {
			want := `{"hookSpecificOutput":{"additionalContext":` + quote(t, text) + `,"hookEventName":"` + event + `"}}` + "\n"
			if got := e.Render(event, []byte(text)); string(got) != want {
				t.Errorf("Render(%q) = %q; want %q", event, got, want)
			}
		}
	})
}

// quote is the JSON string of s as encoding/json writes it.
func quote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

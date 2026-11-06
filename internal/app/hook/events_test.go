package hook

import (
	"encoding/json"
	"slices"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// fakeAgent stands in for an agent's Events and Lead in these tests, which
// cannot import the adapters. It does what the two adapters do: the "claude"
// one reads the mode from the payload, the "codex" one from the transcript.
type fakeAgent struct{ name string }

var (
	_ Events = fakeAgent{}
	_ Lead   = fakeAgent{}
)

// withAgent sets e's agent together with its Events and Lead.
func withAgent(e Env, name string) Env {
	e.Agent, e.Events, e.Lead = name, fakeAgent{name}, fakeAgent{name}
	return e
}

func (fakeAgent) Render(event string, text []byte) []byte {
	if len(text) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": string(text)}})
	return append(encoded, '\n')
}

func (a fakeAgent) Mode(payload []byte, transcript string) (mode.Mode, bool) {
	if a.name == "codex" {
		state, _ := session.ReadLeadState(a.name, transcript)
		return state.Mode, state.Mode != ""
	}
	var h struct {
		PermissionMode string `json:"permission_mode"`
		AgentID        string `json:"agent_id"`
	}
	if json.Unmarshal(payload, &h) != nil || h.PermissionMode == "" || h.AgentID != "" {
		return "", false
	}
	return mode.Parse(a.name, h.PermissionMode), true
}

func (a fakeAgent) Interactive(environ []string, transcript string) bool {
	if a.name == "codex" {
		return session.Originator(transcript) == "codex-tui"
	}
	return slices.Contains(environ, "CLAUDE_CODE_ENTRYPOINT=cli")
}

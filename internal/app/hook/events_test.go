package hook

import (
	"encoding/json"
	"slices"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/app/roster"
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

// withAgent sets e's agent together with its Events and Lead, and the real
// Caps of the registered agents, which read the temporary home the tests set.
func withAgent(e Env, name string) Env {
	e.Agent, e.Events, e.Lead = name, fakeAgent{name}, fakeAgent{name}
	if name == "claude" {
		e.Events = fakeStopAgent{fakeAgent{name}}
		e.OneSessionPerProcess = true
	}
	e.Caps = map[string]roster.Caps{}
	for agent, c := range launchtest.AllCaps() {
		e.Caps[agent] = c
	}
	return e
}

type fakeStopAgent struct{ fakeAgent }

func (fakeStopAgent) BlockStop(reason string) []byte {
	encoded, _ := json.Marshal(map[string]string{"decision": "block", "reason": reason})
	return append(encoded, '\n')
}

func (fakeAgent) Show(out []byte, text string, _ bool) []byte {
	return addFields(out, map[string]string{"systemMessage": text})
}

// addFields sets string fields in the JSON object out, as kit.AddFields does
// for the adapters; an app test may not import an adapter.
func addFields(out []byte, fields map[string]string) []byte {
	object := map[string]any{}
	if len(out) > 0 && json.Unmarshal(out, &object) != nil {
		return out
	}
	for k, v := range fields {
		object[k] = v
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return out
	}
	return append(encoded, '\n')
}

// Show of the "claude" fake adds the OSC 9 terminalSequence on an alert, as
// Claude's Events do; Codex hooks have none.
func (fakeStopAgent) Show(out []byte, text string, alert bool) []byte {
	fields := map[string]string{"systemMessage": text}
	if alert {
		fields["terminalSequence"] = "\x1b]9;" + text + "\a"
	}
	return addFields(out, fields)
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

// Spare is the "claude" fake's bg spare check, as Claude's Lead has it; a
// "codex" one is never a spare.
func (a fakeAgent) Spare(environ []string) bool {
	return a.name == "claude" && slices.Contains(environ, "CLAUDE_CODE_SESSION_KIND=bg") && slices.Contains(environ, "CLAUDE_BG_BACKEND=daemon")
}

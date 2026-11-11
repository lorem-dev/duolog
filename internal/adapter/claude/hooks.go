package claude

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// Events renders Claude Code's hook output and reads its permission mode.
type Events struct{}

// BlockStop asks the lead to continue instead of ending its turn.
func (Events) BlockStop(reason string) []byte {
	if reason == "" {
		return nil
	}
	encoded, _ := json.Marshal(map[string]string{"decision": "block", "reason": reason})
	return append(encoded, '\n')
}

// Show adds text to out as systemMessage, the warning line Claude Code shows
// the person. It adds no context, so a Stop that carries it does not keep the
// turn going. With alert it also adds terminalSequence, an OSC 9 notice that
// Claude Code writes to the person's terminal in an interactive session.
//
// terminalSequence needs no version gate. Claude Code 2.0.76, 2.1.20 and
// 2.1.100 (npm) do not know the key, and read hook output with a zod object
// that is not strict: an unknown key is dropped, and systemMessage still
// shows. None of them rejects the output for it.
func (Events) Show(out []byte, text string, alert bool) []byte {
	fields := map[string]string{"systemMessage": text}
	if alert {
		fields["terminalSequence"] = alertSequence(text)
	}
	return kit.AddFields(out, fields)
}

// alertSequence is the OSC 9 notice of text. Only OSC 9 goes out: a BEL
// fallback after it would ring twice in a terminal that shows OSC 9.
func alertSequence(text string) string {
	return "\x1b]9;" + clean(text) + "\a"
}

// clean drops every control character of text, C1 controls and line breaks
// included, so that the text cannot end the sequence or start another.
func clean(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, text)
}

// Render wraps text in the hookSpecificOutput envelope that Claude Code reads
// from a synchronous hook. asyncRewake instead requires exit 2 and plain
// stderr, so it does not come through here.
func (Events) Render(event string, text []byte) []byte {
	if len(text) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": string(text)}})
	return append(encoded, '\n')
}

// Mode reads permission_mode from the payload. Only the lead's own
// main-thread payloads say its mode: a subagent's definition can set a mode of
// its own, such as bypassPermissions, and its payload carries an agent_id.
func (Events) Mode(payload []byte, _ string) (mode.Mode, bool) {
	var h struct {
		PermissionMode string `json:"permission_mode"`
		AgentID        string `json:"agent_id"`
	}
	if json.Unmarshal(payload, &h) != nil || h.PermissionMode == "" || h.AgentID != "" {
		return "", false
	}
	return mode.Parse(Facts.Name, h.PermissionMode), true
}

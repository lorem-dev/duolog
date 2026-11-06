package claude

import (
	"encoding/json"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// Events renders Claude Code's hook output and reads its permission mode.
type Events struct{}

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

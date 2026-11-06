package claude

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/app/hook/hooktest"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestEventsContract(t *testing.T) {
	hooktest.EventsContract(t, func(*testing.T) hooktest.Events { return Events{} })
}

func TestEventsModeReadsThePayload(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          mode.Mode
		ok            bool
	}{
		{"lead", `{"session_id":"s","permission_mode":"acceptEdits"}`, mode.Edits, true},
		{"subagent", `{"session_id":"s","permission_mode":"bypassPermissions","agent_id":"sub-1"}`, "", false},
		{"no mode", `{"session_id":"s"}`, "", false},
		{"not json", `nope`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := (Events{}).Mode([]byte(tc.payload), "ignored")
			if got != tc.want || ok != tc.ok {
				t.Errorf("Mode = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

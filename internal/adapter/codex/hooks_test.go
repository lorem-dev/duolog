package codex

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/app/hook/hooktest"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestEventsContract(t *testing.T) {
	hooktest.EventsContract(t, func(*testing.T) hooktest.Events { return Events{} })
}

func TestEventsModeReadsTheTranscript(t *testing.T) {
	path := writeRollout(t, t.TempDir(), "mode-1", "codex-tui")
	payload := []byte(`{"session_id":"mode-1","permission_mode":"bypassPermissions"}`)
	if got, ok := (Events{}).Mode(payload, path); !ok || got != mode.Edits {
		t.Errorf("Mode = %q, %v; want %q, true", got, ok, mode.Edits)
	}
	if got, ok := (Events{}).Mode(payload, ""); ok || got != "" {
		t.Errorf("Mode without a transcript = %q, %v; want none", got, ok)
	}
}

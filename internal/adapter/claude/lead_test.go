package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/lead/leadtest"
	corelead "github.com/lorem-dev/duolog/internal/core/lead"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func writeTranscript(t *testing.T, home, id, body string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "projects", "-work-app", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLeadContract(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	leadtest.LeadContract(t, func(*testing.T) leadtest.Lead { return Lead{} }, leadtest.Case{
		IDEnv:          "CLAUDE_CODE_SESSION_ID",
		InteractiveEnv: []string{"CLAUDE_CODE_ENTRYPOINT=cli"},
		Write: func(t *testing.T, home, id string) string {
			return writeTranscript(t, home, id, `{"type":"user","permissionMode":"acceptEdits"}`+"\n"+
				`{"type":"assistant","message":{"model":"claude-sonnet-5-5"}}`+"\n")
		},
		Want: corelead.LeadState{Model: "claude-sonnet-5-5", Mode: mode.Edits},
	})
}

func TestLeadTranscriptFindsTheNewestOfTheID(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	want := writeTranscript(t, home, "abc-123", "")
	if got, ok := (Lead{}).Transcript(home, "abc-123"); !ok || got != want {
		t.Fatalf("Transcript = %q, %v; want %q", got, ok, want)
	}
	for _, id := range []string{"missing", "*", "../abc-123", ""} {
		if got, ok := (Lead{}).Transcript(home, id); ok || got != "" {
			t.Fatalf("id %q found %q", id, got)
		}
	}
}

func TestLeadInteractiveNeedsTheTerminalEntrypoint(t *testing.T) {
	if (Lead{}).Interactive([]string{"CLAUDE_CODE_ENTRYPOINT=sdk-cli"}, "") {
		t.Fatal("an SDK session counted as interactive")
	}
}

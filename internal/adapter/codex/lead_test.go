package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/lead/leadtest"
	corelead "github.com/lorem-dev/duolog/internal/core/lead"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func writeRollout(t *testing.T, home, id, originator string) string {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions", "2026", "11", "05", "rollout-2026-11-05T10-00-00-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session_meta","payload":{"originator":"` + originator + `"}}` + "\n" +
		`{"type":"turn_context","payload":{"model":"gpt-6.1-sol","effort":"high","approval_policy":"on-request","sandbox_policy":{"type":"workspace-write"}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLeadContract(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	leadtest.LeadContract(t, func(*testing.T) leadtest.Lead { return Lead{} }, leadtest.Case{
		IDEnv: "CODEX_THREAD_ID",
		Write: func(t *testing.T, home, id string) string { return writeRollout(t, home, id, "codex-tui") },
		Want:  corelead.LeadState{Model: "gpt-6.1-sol", Mode: mode.Edits},
	})
}

func TestLeadTranscriptFindsTheRolloutOfTheID(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	want := writeRollout(t, home, "def-456", "codex-tui")
	if got, ok := (Lead{}).Transcript(home, "def-456"); !ok || got != want {
		t.Fatalf("Transcript = %q, %v; want %q", got, ok, want)
	}
	for _, id := range []string{"missing", "*", "../def-456", ""} {
		if got, ok := (Lead{}).Transcript(home, id); ok || got != "" {
			t.Fatalf("id %q found %q", id, got)
		}
	}
}

func TestLeadInteractiveNeedsTheTerminalUI(t *testing.T) {
	home := t.TempDir()
	if (Lead{}).Interactive(nil, writeRollout(t, home, "exec-1", "codex_exec")) {
		t.Fatal("a codex exec thread counted as interactive")
	}
	if (Lead{}).Interactive(nil, "") {
		t.Fatal("a thread with no rollout counted as interactive")
	}
}

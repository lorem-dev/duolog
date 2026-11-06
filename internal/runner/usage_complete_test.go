package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// Usage is complete only when the vendor reported the whole run's: Claude's
// result event with usage, or a finished Codex turn. A Claude run cancelled
// or timed out mid-request knows its finished messages' usage only.
func TestUsageCompleteOnlyWithTheRunsTotal(t *testing.T) {
	assistant := `{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":40,"output_tokens":5}}}` + "\n"
	for name, c := range map[string]struct {
		stream          string
		known, complete bool
	}{
		"interrupted after a message": {assistant, true, false},
		"result without usage":        {assistant + `{"type":"result","subtype":"success","structured_output":` + goodAnswer + "}\n", true, false},
		"result with usage":           {assistant + `{"type":"result","subtype":"success","usage":{"input_tokens":40,"output_tokens":5},"structured_output":` + goodAnswer + "}\n", true, true},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "claude.stdout.json")
			if err := os.WriteFile(path, []byte(c.stream), 0600); err != nil {
				t.Fatal(err)
			}
			var r Result
			_, _ = readClaudeStream(path, &r)
			if r.UsageKnown != c.known || r.UsageComplete != c.complete {
				t.Fatalf("known %v complete %v, want %v %v", r.UsageKnown, r.UsageComplete, c.known, c.complete)
			}
		})
	}
	t.Run("codex turn completed", func(t *testing.T) {
		var r Result
		codexEvent([]byte(`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":1}}`), &r)
		if !r.UsageKnown || !r.UsageComplete {
			t.Fatalf("known %v complete %v after a finished Codex turn", r.UsageKnown, r.UsageComplete)
		}
	})
}

package leadtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	corelead "github.com/lorem-dev/duolog/internal/core/lead"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestLeadContract(t *testing.T) {
	want := corelead.LeadState{Model: "fake-model", Mode: mode.Edits}
	LeadContract(t, func(*testing.T) Lead { return &Fake{} }, Case{
		IDEnv:          "FAKE_SESSION_ID",
		InteractiveEnv: []string{"FAKE_INTERACTIVE=1"},
		Write: func(t *testing.T, home, id string) string {
			t.Helper()
			data, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, id+".jsonl")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		},
		Want: want,
	})
}

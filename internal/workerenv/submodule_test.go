package workerenv_test

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// A worker in a duolog submodule must not read the lead state of its lead
// home, the superproject's .duolog, nor its own project's work state.
func TestSubmoduleProtectsBothStateHomes(t *testing.T) {
	f := submodule.New(t)
	for _, root := range []string{f.Superproject, f.Submodule} {
		if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("version: 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	extra := map[string]any{"sandbox": map[string]any{"enabled": false}}
	for _, stateDir := range []string{f.Submodule, project.StateRoot(f.Linked)} {
		leadHome := pathutil.Slashes(project.LeadHome(stateDir))
		workState := pathutil.Slashes(filepath.Join(stateDir, ".duolog"))
		if leadHome == workState || !strings.HasPrefix(workState, strings.TrimSuffix(leadHome, ".duolog")) {
			t.Fatalf("fixture shares no lead home: %s for %s", leadHome, workState)
		}
		for _, raw := range []string{
			workerenv.ClaudeSettings(stateDir, false, extra),
			workerenv.ClaudeSettingsWithRoots(stateDir, true, extra, []string{"/private/usage"}),
		} {
			assertBothStateHomes(t, raw, leadHome, workState)
		}
	}
}

func assertBothStateHomes(t *testing.T, raw, leadHome, workState string) {
	t.Helper()
	var s struct {
		Sandbox struct {
			Enabled    *bool `json:"enabled"`
			Filesystem struct {
				DenyRead   []string `json:"denyRead"`
				AllowWrite []string `json:"allowWrite"`
			} `json:"filesystem"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	denied := s.Sandbox.Filesystem.DenyRead
	for _, name := range []string{"sessions.json", "bus.ndjson", "decisions.ndjson", "cursors"} {
		if !slices.Contains(denied, path.Join(leadHome, name)) {
			t.Fatalf("lead state %s readable: %s", name, raw)
		}
	}
	for _, name := range []string{"usage.ndjson", "journal", "decisions.ndjson"} {
		if !slices.Contains(denied, path.Join(workState, name)) {
			t.Fatalf("work state %s readable: %s", name, raw)
		}
	}
	if s.Sandbox.Enabled == nil || *s.Sandbox.Enabled {
		t.Fatalf("settings changed the sandbox switch: %s", raw)
	}
	if len(s.Sandbox.Filesystem.AllowWrite) != 0 {
		t.Fatalf("settings widened write roots: %s", raw)
	}
}

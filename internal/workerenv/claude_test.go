package workerenv

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestClaudeWorkerSettingsDenyStateWithoutSandbox(t *testing.T) {
	var s struct {
		AutoMemoryEnabled *bool                      `json:"autoMemoryEnabled"`
		Sandbox           map[string]json.RawMessage `json:"sandbox"`
	}
	raw := ClaudeSettings("/p", false, nil)
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if _, on := s.Sandbox["enabled"]; on {
		t.Fatal("duolog must never turn the sandbox on")
	}
	if !strings.Contains(raw, `/p/.duolog/decisions.ndjson`) || !strings.Contains(raw, `/p/.duolog/journal`) {
		t.Fatal(raw)
	}
}

func TestClaudeSettingsPreservesWorkerOptions(t *testing.T) {
	for _, minimal := range []bool{false, true} {
		var s struct {
			AutoMemoryEnabled     *bool    `json:"autoMemoryEnabled"`
			PreferredNotifChannel string   `json:"preferredNotifChannel"`
			ClaudeMdExcludes      []string `json:"claudeMdExcludes"`
			Sandbox               struct {
				Filesystem struct {
					DenyRead []string `json:"denyRead"`
				} `json:"filesystem"`
			} `json:"sandbox"`
		}
		extra := map[string]any{"preferredNotifChannel": "notifications_disabled"}
		raw := ClaudeSettings("/p", minimal, extra)
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatal(err)
		}
		if s.AutoMemoryEnabled == nil || *s.AutoMemoryEnabled || s.PreferredNotifChannel != "notifications_disabled" {
			t.Fatal(raw)
		}
		want := []string{
			"/p/.duolog/decisions.ndjson", "/p/.duolog/requests.ndjson", "/p/.duolog/bus.ndjson",
			"/p/.duolog/usage.ndjson", "/p/.duolog/coauthors.ndjson", "/p/.duolog/sessions.json",
			"/p/.duolog/cursors", "/p/.duolog/journal", "/p/.duolog/routing.learned.yaml",
		}
		if !reflect.DeepEqual(s.Sandbox.Filesystem.DenyRead, want) {
			t.Fatal(raw)
		}
		if minimal {
			if !reflect.DeepEqual(s.ClaudeMdExcludes, []string{"**/CLAUDE.md", "**/CLAUDE.local.md", "**/.claude/rules/**"}) {
				t.Fatal(raw)
			}
		} else if len(s.ClaudeMdExcludes) != 0 {
			t.Fatal(raw)
		}
		if len(extra) != 1 {
			t.Fatal("caller options were changed", extra)
		}
	}
}

func TestClaudeSettingsPinsFastOffAfterExtra(t *testing.T) {
	for _, minimal := range []bool{false, true} {
		var s struct {
			FastMode          *bool    `json:"fastMode"`
			AutoMemoryEnabled *bool    `json:"autoMemoryEnabled"`
			ClaudeMdExcludes  []string `json:"claudeMdExcludes"`
			Sandbox           struct {
				Filesystem struct {
					DenyRead []string `json:"denyRead"`
				} `json:"filesystem"`
			} `json:"sandbox"`
		}
		extra := map[string]any{"fastMode": true}
		raw := ClaudeSettings("/p", minimal, extra)
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatal(err)
		}
		if s.FastMode == nil || *s.FastMode {
			t.Fatal("fast mode must be pinned off", raw)
		}
		if s.AutoMemoryEnabled == nil || *s.AutoMemoryEnabled {
			t.Fatal(raw)
		}
		if !slices.Contains(s.Sandbox.Filesystem.DenyRead, "/p/.duolog/decisions.ndjson") {
			t.Fatal(raw)
		}
		if (len(s.ClaudeMdExcludes) > 0) != minimal {
			t.Fatal(minimal, raw)
		}
		if extra["fastMode"] != true {
			t.Fatal("caller options were changed", extra)
		}
	}
}

func TestClaudeSettingsEmptyStateKeepsSandboxUnchanged(t *testing.T) {
	raw := ClaudeSettings("", true, nil)
	var settings map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatal(err)
	}
	if _, ok := settings["sandbox"]; ok {
		t.Fatal(raw)
	}
	if string(settings["autoMemoryEnabled"]) != "false" || len(settings["claudeMdExcludes"]) == 0 {
		t.Fatal(raw)
	}
}

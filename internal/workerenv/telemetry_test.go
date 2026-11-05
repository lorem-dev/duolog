package workerenv

import (
	"encoding/json"
	"reflect"
	"testing"
)

// settingsOut is the part of the worker settings these tests read.
type settingsOut struct {
	AutoMemoryEnabled     *bool    `json:"autoMemoryEnabled"`
	PreferredNotifChannel string   `json:"preferredNotifChannel"`
	ClaudeMdExcludes      []string `json:"claudeMdExcludes"`
	Sandbox               *struct {
		Enabled    *bool `json:"enabled"`
		Filesystem struct {
			DenyRead []string `json:"denyRead"`
		} `json:"filesystem"`
	} `json:"sandbox"`
}

func decodeSettings(t *testing.T, raw string) (settingsOut, map[string]any) {
	t.Helper()
	var s settingsOut
	var all map[string]any
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &all); err != nil {
		t.Fatal(err)
	}
	return s, all
}

var stateDenies = []string{
	"/p/.duolog/decisions.ndjson", "/p/.duolog/requests.ndjson", "/p/.duolog/bus.ndjson",
	"/p/.duolog/usage.ndjson", "/p/.duolog/coauthors.ndjson", "/p/.duolog/sessions.json",
	"/p/.duolog/cursors", "/p/.duolog/journal", "/p/.duolog/routing.learned.yaml",
}

func TestTelemetryDenyReadSettings(t *testing.T) {
	telemetryRoot := "/home/u/.local/state/duolog/telemetry"
	balanceRoot := "/home/u/.local/state/duolog/balance"
	// A repeated root, a root the caller already denies and a root in Windows
	// form each appear once, in slash form.
	roots := []string{telemetryRoot, balanceRoot, telemetryRoot + "/", `C:\Users\u\AppData\Local\duolog\telemetry`}
	paths := map[string]map[string]any{
		"worker": nil,
		"peer":   {"preferredNotifChannel": "notifications_disabled"},
		"peer with denies": {
			"preferredNotifChannel": "notifications_disabled",
			"sandbox":               map[string]any{"filesystem": map[string]any{"denyRead": []any{"/secret", balanceRoot}}},
		},
	}
	for name, extra := range paths {
		for _, minimal := range []bool{false, true} {
			before, _ := json.Marshal(extra)
			raw := ClaudeSettingsWithRoots("/p", minimal, extra, roots)
			s, all := decodeSettings(t, raw)
			var want []string
			if extra["sandbox"] != nil {
				want = append(want, "/secret", balanceRoot)
				want = append(want, stateDenies...)
				want = append(want, telemetryRoot, "C:/Users/u/AppData/Local/duolog/telemetry")
			} else {
				want = append(append(want, stateDenies...), telemetryRoot, balanceRoot, "C:/Users/u/AppData/Local/duolog/telemetry")
			}
			if s.Sandbox == nil || !reflect.DeepEqual(s.Sandbox.Filesystem.DenyRead, want) {
				t.Fatalf("%s minimal=%v: %s", name, minimal, raw)
			}
			if s.AutoMemoryEnabled == nil || *s.AutoMemoryEnabled {
				t.Fatal(name, raw)
			}
			if wantChannel := map[bool]string{true: "notifications_disabled"}[name != "worker"]; s.PreferredNotifChannel != wantChannel {
				t.Fatal(name, raw)
			}
			if minimal != (len(s.ClaudeMdExcludes) == 3) {
				t.Fatal(name, minimal, raw)
			}
			if _, on := all["sandbox"].(map[string]any)["enabled"]; on {
				t.Fatal(name, raw)
			}
			if after, _ := json.Marshal(extra); string(after) != string(before) {
				t.Fatalf("caller options were changed: %s", after)
			}
			// Without roots the settings are those of ClaudeSettings.
			if got, plain := ClaudeSettingsWithRoots("/p", minimal, extra, nil), ClaudeSettings("/p", minimal, extra); got != plain {
				t.Fatal(got, plain)
			}
		}
	}
	// Roots are denied even without a project state folder.
	s, _ := decodeSettings(t, ClaudeSettingsWithRoots("", true, nil, []string{telemetryRoot, "", balanceRoot}))
	if s.Sandbox == nil || !reflect.DeepEqual(s.Sandbox.Filesystem.DenyRead, []string{telemetryRoot, balanceRoot}) {
		t.Fatal(s.Sandbox)
	}
}

// TestTelemetrySandboxOffStaysOff: the denies only protect a sandbox the
// person turned on; duolog never inserts or changes sandbox.enabled.
func TestTelemetrySandboxOffStaysOff(t *testing.T) {
	roots := []string{"/t/telemetry", "/t/balance"}
	for name, c := range map[string]struct {
		extra   map[string]any
		enabled any
	}{
		"missing":       {nil, nil},
		"empty":         {map[string]any{"sandbox": map[string]any{}}, nil},
		"disabled":      {map[string]any{"sandbox": map[string]any{"enabled": false, "allowUnsandboxedCommands": true}}, false},
		"disabled list": {map[string]any{"sandbox": map[string]any{"enabled": false, "filesystem": map[string]any{"denyRead": []string{"/x"}, "allowWrite": []any{"/w"}}}}, false},
	} {
		for _, stateDir := range []string{"", "/p"} {
			raw := ClaudeSettingsWithRoots(stateDir, false, c.extra, roots)
			_, all := decodeSettings(t, raw)
			sandbox, _ := all["sandbox"].(map[string]any)
			enabled, has := sandbox["enabled"]
			if c.enabled == nil && has || c.enabled != nil && enabled != c.enabled {
				t.Fatalf("%s: sandbox.enabled changed: %s", name, raw)
			}
			if name == "disabled" && sandbox["allowUnsandboxedCommands"] != true {
				t.Fatal(name, raw)
			}
			fs, _ := sandbox["filesystem"].(map[string]any)
			if name == "disabled list" && !reflect.DeepEqual(fs["allowWrite"], []any{"/w"}) {
				t.Fatal(name, raw)
			}
			denied, _ := fs["denyRead"].([]any)
			if len(denied) < 2 || denied[len(denied)-2] != "/t/telemetry" || denied[len(denied)-1] != "/t/balance" {
				t.Fatal(name, raw)
			}
		}
	}
}

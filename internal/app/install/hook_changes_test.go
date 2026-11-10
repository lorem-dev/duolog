package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kills mutations that omit timeout-only updates, new events, or the file path
// from notes, and that report foreign handlers as duolog changes.
func TestCodexHookUpgradeNotes(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		t.Run(scope, func(t *testing.T) {
			home, root := setup(t)
			base := root
			if scope == "user" {
				base = home
			}
			path := filepath.Join(base, ".codex", "hooks.json")
			old, err := HooksJSON("codex", 150)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(old, &doc); err != nil {
				t.Fatal(err)
			}
			hooks := doc["hooks"].(map[string]any)
			delete(hooks, "Stop")
			hooks["PreToolUse"] = append([]any{map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "other", "timeout": 12}}}}, hooks["PreToolUse"].([]any)...)
			old, err = json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, old, 0o644); err != nil {
				t.Fatal(err)
			}
			r := run(t, Options{Scope: scope, Agent: "codex", Home: home, Root: root})
			text := strings.Join(r.Notes, "\n")
			suffix := "Codex skips the changed hook until you approve it in /hooks; duolog cannot approve it for you."
			for _, event := range []string{"PreToolUse", "PermissionRequest"} {
				want := "duolog changed " + event + " in " + path + ": timeout 150 -> 630. " + suffix
				if !strings.Contains(text, want) {
					t.Errorf("notes missing %q: %s", want, text)
				}
			}
			if want := "duolog added Stop in " + path + ": new hook"; !strings.Contains(text, want) {
				t.Errorf("notes missing %q: %s", want, text)
			}
			if strings.Contains(text, "12 ->") || strings.Contains(text, "changed SessionStart") {
				t.Errorf("notes include unchanged or foreign hooks: %s", text)
			}
			again := run(t, Options{Scope: scope, Agent: "codex", Home: home, Root: root})
			for _, note := range again.Notes {
				if strings.Contains(note, "duolog changed") || strings.Contains(note, "duolog added") {
					t.Errorf("unchanged install: %s", note)
				}
			}
		})
	}
}

// Kills mutations that treat an existing foreign hooks file as a duolog upgrade.
func TestCodexFreshHookNote(t *testing.T) {
	home, root := setup(t)
	path := filepath.Join(root, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"other"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, Options{Agent: "codex", Home: home, Root: root})
	want := "Codex: approve the duolog hooks in /hooks (press t), and trust this project, or Codex will not run them."
	found := false
	for _, note := range r.Notes {
		found = found || note == want
		if strings.Contains(note, "duolog changed") || strings.Contains(note, "duolog added") {
			t.Errorf("fresh install: %s", note)
		}
	}
	if !found {
		t.Errorf("missing fresh install note: %q", r.Notes)
	}
}

// Kills mutations that compare only gate commands or omit Windows, matcher,
// missing fields, and non-gate hook changes from the install report.
func TestCodexHookChangedFields(t *testing.T) {
	for _, event := range []string{"PreToolUse", "SessionStart"} {
		t.Run(event, func(t *testing.T) {
			home, root := setup(t)
			path := filepath.Join(root, ".codex", "hooks.json")
			old := `{"hooks":{"` + event + `":[{"matcher":"Bash","hooks":[{"type":"command","command":"duolog hook old","commandWindows":"duolog hook windows","timeout":150,"async":true}]}]}}`
			write(t, path, old)
			r := run(t, Options{Agent: "codex", Home: home, Root: root})
			text := strings.Join(r.Notes, "\n")
			for _, want := range []string{
				"duolog changed " + event + " in " + path + ": ",
				`command "duolog hook old" -> "if command -v duolog`,
				`commandWindows "duolog hook windows" -> "powershell`,
				`group.matcher "Bash" -> (absent)`,
				`async true -> (absent)`,
			} {
				if !strings.Contains(text, want) {
					t.Errorf("notes missing %q: %s", want, text)
				}
			}
			timeout := "timeout 150 -> 630"
			if event == "SessionStart" {
				timeout = "timeout 150 -> (absent)"
			}
			if !strings.Contains(text, timeout) {
				t.Errorf("notes missing %q: %s", timeout, text)
			}
		})
	}
}

// Kills recording a group field and a handler field of the same name under
// one path, which hides one of the two changes.
func TestCodexHookGroupAndHandlerFieldsStayApart(t *testing.T) {
	home, root := setup(t)
	path := filepath.Join(root, ".codex", "hooks.json")
	write(t, path, `{"hooks":{"PreToolUse":[{"timeout":17,"hooks":[{"type":"command","command":"duolog gate old","timeout":150}]}]}}`)
	r := run(t, Options{Agent: "codex", Home: home, Root: root})
	text := strings.Join(r.Notes, "\n")
	for _, want := range []string{"group.timeout 17 -> (absent)", "timeout 150 -> 630"} {
		if !strings.Contains(text, want) {
			t.Errorf("notes missing %q: %s", want, text)
		}
	}
}

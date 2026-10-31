package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

func TestNormalizeClaudeAndCodex(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		data, err := os.ReadFile(filepath.Join("testdata", agent+"-pretooluse.json"))
		if err != nil {
			t.Fatal(err)
		}
		a, err := Normalize(agent, data)
		if err != nil {
			t.Fatal(err)
		}
		// The recorded requests differ only in the file touched.
		want := "touch probe.txt"
		if agent == "codex" {
			want = "touch probe_c.txt"
		}
		if a != (Action{Kind: "exec", Target: want}) {
			t.Fatal(a)
		}
	}
	{
		root := t.TempDir()
		data, _ := json.Marshal(map[string]any{"tool_name": "Read", "tool_input": map[string]string{"file_path": filepath.Join(root, "src", "..", "a.go")}, "cwd": root})
		a, err := Normalize("claude", data)
		if err != nil || a.Kind != "read" || a.Target != "a.go" {
			t.Fatal(a, err)
		}
	}
}
func TestCodexToolNames(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ tool, input, kind, want string }{
		{"Bash", `{"command":"go test ./..."}`, "exec", Allow},
		{"collaborationspawn_agent", `{}`, "spawn", Deny},
		{"apply_patch", `{"patch":"*** Begin Patch"}`, "write", Ask},
		{"unknown", `{}`, "unknown", Ask},
		{"Write", `{"file_path":"src/a.go"}`, "unknown", Ask},
	} {
		a, err := Normalize("codex", []byte(`{"tool_name":"`+tc.tool+`","tool_input":`+tc.input+`}`))
		if err != nil || a.Kind != tc.kind {
			t.Fatal(a, err)
		}
		d := Evaluate(p, LeadRules{}, mode.Full, a)
		if d.Kind != tc.want {
			t.Fatal(tc.tool, d)
		}
	}
}
func TestNormalizeRejectsMalformed(t *testing.T) {
	for _, data := range []string{`{`, `{}`, `{"tool_name":"Bash","tool_input":{}}`, `{"tool_name":"Read","tool_input":{"file_path":5}}`} {
		if _, err := Normalize("codex", []byte(data)); err == nil {
			t.Fatal(data)
		}
	}
}

func TestUnknownToolsAskInEveryMode(t *testing.T) {
	p := policy(t)
	for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
		check(t, p, LeadRules{}, m, Action{"unknown", "unverified-tool", "implement"}, Ask)
	}
}
func TestRenderMatchesEachAgentFormat(t *testing.T) {
	data, err := os.ReadFile("testdata/hook-output-honored.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]json.RawMessage
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "codex"} {
		for _, tc := range []struct{ event, kind, key string }{
			{"PreToolUse", Deny, "claude_and_codex_pretooluse_deny_json"},
			{"PreToolUse", Ask, "claude_and_codex_pretooluse_deny_json"},
			{"PermissionRequest", Deny, "claude_and_codex_permissionrequest_deny"},
			{"PermissionRequest", Ask, "claude_and_codex_permissionrequest_deny"},
			{"PermissionRequest", Allow, "claude_and_codex_permissionrequest_allow"},
		} {
			var got, want any
			if err = json.Unmarshal(Render(agent, tc.event, Decision{Kind: tc.kind, Reason: "reason text"}), &got); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(fixtures[tc.key], &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s %s %s: %v != %v", agent, tc.event, tc.kind, got, want)
			}
		}
		if got := Render(agent, "PreToolUse", Decision{Kind: Allow}); len(got) != 0 {
			t.Fatalf("unverified allow: %s", got)
		}
	}
}

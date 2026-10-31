package gate

import (
	"encoding/json"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"path/filepath"
	"testing"
)

func TestPowerShellPolicyOnEveryHost(t *testing.T) {
	p := policy(t)
	for _, tc := range []struct{ command, role, want string }{
		{"Get-Content src/file.txt", "review_final", Allow},
		{"GET-CHILDITEM src", "review_final", Allow},
		{`Get-Content src\file.txt`, "review_final", Allow},
		{"git status", "review_final", Allow},
		{"Get-Location", "review_final", Allow},
		{"Set-Content file text", "review_final", Deny},
		{"go test ./...", "review_final", Deny},
		{"Remove-Item -Recurse build", "implement", Deny},
		{"REMOVE-ITEM build -RECURSE -Force", "implement", Deny},
		{"Git Push origin main", "implement", Deny},
		{"Get-Content file; Write-Output x", "implement", Ask},
		{"Get-Content file | Set-Content other", "implement", Ask},
		{"& git status", "implement", Ask},
		{"Get-Content $env:SECRET", "implement", Ask},
		{"Get-Content 'file'", "implement", Ask},
		{"Get-Content (Get-Item file)", "implement", Ask},
		{"Get-Content file > other", "implement", Ask},
		{"Get-Content file`nSet-Content x y", "implement", Ask},
		{"{ Get-Content file }", "implement", Ask},
	} {
		input, _ := json.Marshal(map[string]any{"tool_name": "PowerShell", "tool_input": map[string]string{"command": tc.command}})
		for _, agent := range []string{"claude", "codex"} {
			a, err := Normalize(agent, input)
			if err != nil || a.Kind != "exec" {
				t.Fatal(a, err)
			}
			a.Role = tc.role
			if got := Evaluate(p, LeadRules{}, mode.Full, a); got.Kind != tc.want {
				t.Errorf("%s %q: %+v want %s", agent, tc.command, got, tc.want)
			}
		}
	}
}

func TestWindowsHookTargetsAndDenies(t *testing.T) {
	for _, tool := range []string{"Read", "Write", "Edit", "MultiEdit", "NotebookEdit", "Grep", "Glob", "LS"} {
		input, _ := json.Marshal(map[string]any{"tool_name": tool, "cwd": `C:\Project`, "tool_input": map[string]string{"file_path": `c:\PROJECT\SRC\Index.ts`}})
		a, err := Normalize("claude", input)
		if err != nil || a.Target != "src/index.ts" {
			t.Fatal(tool, a, err)
		}
	}
	for _, tc := range []struct{ root, cwd, target, want string }{
		{`C:\Project`, `c:\project`, `C:\PROJECT\src\index.ts`, "src/index.ts"},
		{`C:\Project`, `C:\Project\src`, `..\private\Key.txt`, "private/key.txt"},
		{`C:\Project`, `C:\Project`, `D:\private\key`, "d:/private/key"},
	} {
		if got := RelativeTarget(tc.root, tc.cwd, tc.target); got != tc.want {
			t.Errorf("%+v: %q", tc, got)
		}
	}
	for _, tc := range []struct{ pattern, target string }{
		{`SRC\**`, `src\INDEX.ts`},
		{`src/**`, `SRC\INDEX.ts`},
		{`C:\Project\PRIVATE\**`, `c:\project\private\key`},
	} {
		p := config.Policy{OnUnmatched: Allow, Rules: []config.Rule{{Action: "read", Match: tc.pattern, Decision: Deny}}}
		if got := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "read", Target: tc.target}); got.Kind != Deny {
			t.Fatal(tc, got)
		}
	}
	rule, err := claudeDeny("PowerShell(git push:*)")
	if err != nil || rule.Action != "exec" {
		t.Fatal(rule, err)
	}
	if got, err := claudePathPattern(`C:\PROJECT\Private\**`, `c:\project`, `C:\Users\me`, `C:\project`); err != nil || got != "private/**" {
		t.Fatal(got, err)
	}
}

func TestClaudeConfigOverride(t *testing.T) {
	_ = policy(t)
	root := t.TempDir()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	writeSetting(t, filepath.Join(dir, "settings.json"), `{"permissions":{"deny":["PowerShell(git push:*)","Read(./private/**)"]}}`)
	rules, err := ImportLead(root, "claude")
	if err != nil || len(rules.Deny) != 2 {
		t.Fatal(rules, err)
	}
}

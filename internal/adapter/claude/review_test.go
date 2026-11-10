package claude

import (
	"path/filepath"
	"testing"
)

func TestOrdinaryGateRejectsAsyncAndManagedSuppression(t *testing.T) {
	// Mutation killed: ordinary workers bypass effective synchronous hook verification.
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "async", true: "managed"}[managed], func(t *testing.T) {
			fixture(t)
			root := t.TempDir()
			gate := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse","timeout":630,"async":true}]}]}}`
			writeFile(t, filepath.Join(root, ".claude", "settings.json"), gate)
			old := managedFiles
			t.Cleanup(func() { managedFiles = old })
			managedFiles = func(string, string, []string) []string { return nil }
			if managed {
				path := filepath.Join(t.TempDir(), "managed.json")
				writeFile(t, path, `{"allowManagedHooksOnly":true}`)
				managedFiles = func(string, string, []string) []string { return []string{path} }
			}
			state, err := hooks(root)
			if err != nil || (Trust{}).GateProblem(state, true) == "" {
				t.Fatalf("%+v %v", state, err)
			}
		})
	}
}

func TestPaneDoesNotTransferApprovalToChangedDefinition(t *testing.T) {
	// Mutation killed: matching only a server name enables a changed startup command.
	r := newStartRig(t)
	r.state(map[string]any{"enabledMcpjsonServers": []string{"a"}})
	r.write(filepath.Join(r.lead, ".mcp.json"), `{"mcpServers":{"a":{"command":"approved"}}}`)
	r.want(nil, []string{"a", "b"})
}

package trust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// managedGate is a settings file whose PreToolUse hook is the gate install
// writes.
const managedGate = `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude","--event","PreToolUse"],"timeout":150}]}]}}`

// Re-review 5, N3: Claude's managed settings decide which hooks run. When
// they set allowManagedHooksOnly or disableAllHooks, the gate in the
// project's own settings does not run, so a held launch is refused unless
// the managed layer holds the gate itself. A gate hook there counts as any
// other, and must be the exact command too.
func TestHeldGateReadsManagedSettings(t *testing.T) {
	home, root, policy := t.TempDir(), t.TempDir(), t.TempDir()
	environ := []string{"HOME=" + home, "USERPROFILE=" + home}
	file, dropIn := filepath.Join(policy, "managed-settings.json"), filepath.Join(policy, "managed-settings.d", "10-hooks.json")
	old := managedFiles
	t.Cleanup(func() { managedFiles = old })
	managedFiles = func(string, string, []string) []string { return []string{file, dropIn} }
	writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), managedGate)
	gate := filepath.Join(home, "bin", "duolog")
	check := func(name, managed, drop string, want bool) {
		t.Helper()
		for path, body := range map[string]string{file: managed, dropIn: drop} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if body != "" {
				writeFile(t, path, body)
			}
		}
		problem := HeldGateProblem(root, environ, gate)
		if (problem == "") != want {
			t.Errorf("%s: problem %q, want proven %v", name, problem, want)
		}
		if !want && !strings.Contains(problem, "managed") {
			t.Errorf("%s: %q does not name the managed settings", name, problem)
		}
	}
	check("no managed settings", "", "", true)
	check("managed settings without hooks", `{"permissions":{"deny":["WebFetch"]}}`, "", true)
	check("managed hooks only", `{"allowManagedHooksOnly":true}`, "", false)
	check("managed hooks off", `{"disableAllHooks":true}`, "", false)
	check("drop-in sets managed hooks only", "", `{"allowManagedHooksOnly":true}`, false)
	check("managed hooks only, gate in managed", `{"allowManagedHooksOnly":true,`+managedGate[1:], "", true)
	check("managed hooks only, gate in a drop-in", `{"allowManagedHooksOnly":true}`, managedGate, true)
	check("managed gate only names the gate", `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"true # duolog gate --agent claude --event PreToolUse"}]}]}}`, "", false)
}

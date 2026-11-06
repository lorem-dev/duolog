package install

import (
	"strings"
	"testing"
)

// Only Claude's lead settings get the PreCompact hook, and the plugin's
// hooks.json carries the same document.
func TestPreCompactHookIsClaudeOnly(t *testing.T) {
	for agent, want := range map[string]bool{"claude": true, "codex": false} {
		data, err := HooksJSON(agent, 150)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(data), `"PreCompact"`); got != want {
			t.Errorf("%s: PreCompact hook %v, want %v", agent, got, want)
		}
	}
}

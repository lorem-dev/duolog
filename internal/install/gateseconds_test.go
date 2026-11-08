package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func hooksFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func gateHook(extra string) string {
	return `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude","--event","PreToolUse"]` + extra + `}]}]}}`
}

func TestGateHookSeconds(t *testing.T) {
	long, old := hooksFile(t, gateHook(`,"timeout":630`)), hooksFile(t, gateHook(`,"timeout":150`))
	foreign := hooksFile(t, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"other","timeout":5}]}]}}`)
	bare := hooksFile(t, gateHook(""))
	missing := filepath.Join(t.TempDir(), "none.json")
	for name, tc := range map[string]struct {
		files []string
		want  float64
		found bool
	}{
		"no files":              {nil, 0, false},
		"a missing file":        {[]string{missing}, 0, false},
		"only a foreign hook":   {[]string{foreign}, 0, false},
		"one gate":              {[]string{long}, 630, true},
		"the shortest counts":   {[]string{long, old, foreign}, 150, true},
		"no timeout is default": {[]string{bare}, AgentHookDefault, true},
	} {
		t.Run(name, func(t *testing.T) {
			got, found := GateHookSeconds(tc.files, "PreToolUse")
			if got != tc.want || found != tc.found {
				t.Fatalf("got %v %v, want %v %v", got, found, tc.want, tc.found)
			}
		})
	}
	if _, found := GateHookSeconds([]string{long}, "PermissionRequest"); found {
		t.Fatal("a hook of another event counted")
	}
}

// Codex hashes the timeout into a hook's approval and skips a hook whose hash
// changed, so a new timeout has to read as a changed gate.
func TestGateCommandsSeeATimeout(t *testing.T) {
	if GateCommands([]byte(gateHook(`,"timeout":150`))) == GateCommands([]byte(gateHook(`,"timeout":630`))) {
		t.Fatal("a changed gate timeout reads as the same gate")
	}
	if GateCommands([]byte(gateHook(""))) == GateCommands([]byte(gateHook(`,"timeout":630`))) {
		t.Fatal("a timeout added to the gate reads as the same gate")
	}
}

// An old hook keeps working: the gate shortens its wait to fit it, and only a
// timeout below the old one is refused.
func TestGateWaitsAcceptsAnOldHook(t *testing.T) {
	timeout := func(v float64) HookGroup {
		var g HookGroup
		if err := json.Unmarshal(fmt.Appendf(nil, `{"hooks":[{"type":"command","timeout":%g}]}`, v), &g); err != nil {
			t.Fatal(err)
		}
		return g
	}
	if _, ok := timeout(MinGateTimeout).GateWaits(0); !ok {
		t.Fatal("a hook from an earlier install is refused")
	}
	if _, ok := timeout(MinGateTimeout - 0.5).GateWaits(0); ok {
		t.Fatal("a hook too short for a lead ask is accepted")
	}
}

package codex

import (
	"runtime"
	"testing"
)

// currentHookHash is Codex's approval hash of the PreToolUse gate handler h
// of group g in the hooks file data.
func currentHookHash(data []byte, g, h int) (string, error) {
	return hookHash(data, "PreToolUse", g, h)
}

// The hash below is the trusted_hash Codex 0.167.1 recorded when the person
// approved this hook in /hooks. A Codex release that changes its hook
// identity changes it, and this test then says the copied approvals would
// be skipped.
func TestHookHashMatchesCodex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Codex hashes commandWindows on Windows")
	}
	data := []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"r=$(git rev-parse --show-toplevel 2>/dev/null); if [ -x \"$r/scripts/dev/bin/duolog\" ]; then exec \"$r\"/scripts/dev/bin/duolog gate --agent codex --event PreToolUse; elif [ \"${DUOLOG_DEPTH:-0}\" -ge 1 ] 2>/dev/null; then exit 2; fi","commandWindows":"powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File scripts/dev/bin/duolog.ps1 gate --agent codex --event PreToolUse","timeout":150}]}]}}`)
	got, err := hookHash(data, "PreToolUse", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:ca3265bc3159e5d6a44cc78c2b2191ee05bcd9563440aa6b273dcea09d92f6a4"; got != want {
		t.Fatalf("hookHash = %s, want Codex's %s", got, want)
	}
}

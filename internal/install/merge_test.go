package install

import "testing"

func TestExecutableHookOwnership(t *testing.T) {
	for _, raw := range []string{
		`{"hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude"]}]}`,
		`{"hooks":[{"type":"command","commandWindows":"powershell -NoProfile -NonInteractive -Command duolog gate --agent codex"}]}`,
	} {
		if _, ours, err := withoutOurHandlers([]byte(raw)); err != nil || !ours {
			t.Fatal("hook not recognized", raw, err)
		}
	}
}

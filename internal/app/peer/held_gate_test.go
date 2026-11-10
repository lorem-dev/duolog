package peer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

// claudeGate installs the duolog gate in the project's local Claude settings,
// or removes it.
func (r *rig) claudeGate(on bool) {
	r.t.Helper()
	path := filepath.Join(r.root, ".claude", "settings.local.json")
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			r.t.Fatal(err)
		}
		return
	}
	writeFile(r.t, path, `{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`)
}

// Codex review 2: a Claude peer at level plan or in a read-only role starts in
// Claude's default mode, so only duolog's gate keeps it read-only. It opens
// only after a gate preflight in the pane's own environment: hooks that are
// missing, or turned off while the settings allow Write, Edit and Bash, open
// no pane, and the account hold is given back.
func TestHeldClaudePeerNeedsItsGate(t *testing.T) {
	inherited := `{"disableAllHooks":true,"permissions":{"allow":["Write","Edit","Bash(*)"]}}`
	for _, tc := range []struct {
		name, role string
		lead       mode.Mode
		setup      func(r *rig)
	}{
		{"read-only role, hooks missing", "review_final", mode.Full, func(r *rig) { r.claudeGate(false) }},
		{"read-only role, hooks off with inherited allows", "review_final", mode.Full, func(r *rig) {
			writeFile(r.t, filepath.Join(r.root, ".claude", "settings.json"), inherited)
		}},
		{"lead in plan, user settings turn hooks off", "plan", mode.Plan, func(r *rig) {
			writeFile(r.t, filepath.Join(os.Getenv("HOME"), ".claude", "settings.json"), inherited)
		}},
	} {
		r := newRig(t, baseScript())
		r.lead("claude", tc.lead)
		tc.setup(r)
		_, err := Open(r.ctx, tc.role)
		if err == nil || !strings.Contains(err.Error(), "gate preflight failed") || !strings.Contains(err.Error(), "nothing ran") {
			t.Errorf("%s: %v", tc.name, err)
		}
		if len(r.spawns()) != 0 {
			t.Errorf("%s: a pane was opened", tc.name)
		}
		if st := r.accounts(); len(st.Reservations) != 0 {
			t.Errorf("%s: a refused pane holds: %+v", tc.name, st.Reservations)
		}
	}
	// Re-review 3: a hook that only names the gate in a comment, or runs a
	// duolog that is not there, does not show the gate.
	missing := filepath.ToSlash(filepath.Join(t.TempDir(), "gone", "duolog"))
	for name, settings := range map[string]string{
		"comment":            `{"permissions":{"allow":["Write","Edit","Bash(*)"]},"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"true # duolog gate --agent claude --event PreToolUse"}]}]}}`,
		"missing executable": `{"permissions":{"allow":["Write","Edit","Bash(*)"]},"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"` + missing + `","args":["gate","--agent","claude","--event","PreToolUse"]}]}]}}`,
	} {
		r := newRig(t, baseScript())
		r.lead("claude", mode.Full)
		writeFile(r.t, filepath.Join(r.root, ".claude", "settings.local.json"), settings)
		_, err := Open(r.ctx, "review_final")
		// A hook that only names the gate in a comment is no gate at all, so
		// its refusal need not point at the hook.
		if err == nil || !strings.Contains(err.Error(), "gate preflight failed") || (name != "comment" && !strings.Contains(err.Error(), "duolog install")) {
			t.Errorf("%s: %v", name, err)
		}
		if len(r.spawns()) != 0 {
			t.Errorf("%s: a pane was opened", name)
		}
	}
	// With its gate shown, the same pane opens.
	r := newRig(t, withScreens(claudeIdle("")))
	r.lead("claude", mode.Full)
	if _, err := Open(r.ctx, "review_final"); err != nil {
		t.Fatal(err)
	}
	if len(r.spawns()) != 1 {
		t.Fatal("no pane was opened")
	}
}

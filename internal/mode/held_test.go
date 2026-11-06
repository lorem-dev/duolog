package mode

import (
	"slices"
	"testing"
)

// Codex review 2, Claude review 3: a Claude worker or peer at level plan or
// in a read-only role starts in Claude's default mode, so only duolog's gate
// keeps it read-only. GateHeld names it whatever Looser says; a Codex worker
// keeps its OS sandbox and is not held.
func TestGateHeld(t *testing.T) {
	for _, tc := range []struct {
		agent   string
		level   Mode
		sandbox string
		want    bool
	}{
		{"claude", Plan, "workspace-write", true},
		{"claude", Ask, "read-only", true},
		{"claude", Full, "read-only", true},
		{"claude", Ask, "workspace-write", false},
		{"claude", Edits, "workspace-write", false},
		{"codex", Plan, "read-only", false},
		{"codex", Plan, "workspace-write", false},
	} {
		if got := GateHeld(tc.agent, tc.level, tc.sandbox); got != tc.want {
			t.Errorf("%s %s %s: %t", tc.agent, tc.level, tc.sandbox, got)
		}
	}
	// The dependency is not Looser: a held worker resolves to duolog's own
	// default for its level.
	r := Resolve("claude", Request{}, Ceiling{Lead: Full, Effective: Full}, "read-only")
	if r.Looser || !GateHeld("claude", r.Level, "read-only") {
		t.Fatalf("%+v", r)
	}
}

// The second wall: a held Claude worker or peer starts without the built-in
// tools that are not read-only and that the gate cannot judge, and a worker
// also without the file write tools. A peer keeps Write and Edit for its own
// report, which the gate allows and judges.
func TestHeldDisallowedTools(t *testing.T) {
	worker, peer := HeldDisallowedTools(false), HeldDisallowedTools(true)
	for _, tool := range []string{"EnterPlanMode", "ExitPlanMode", "CronCreate", "RemoteTrigger", "SendMessage", "EnterWorktree", "Monitor", "NotebookEdit", "MultiEdit"} {
		if !slices.Contains(worker, tool) || !slices.Contains(peer, tool) {
			t.Errorf("%s not disallowed: worker %v peer %v", tool, worker, peer)
		}
	}
	for _, tool := range []string{"Write", "Edit"} {
		if !slices.Contains(worker, tool) || slices.Contains(peer, tool) {
			t.Errorf("%s: worker %v peer %v", tool, worker, peer)
		}
	}
	for _, tool := range []string{"Read", "Grep", "Glob", "Bash", "ToolSearch", "StructuredOutput", "TodoWrite"} {
		if slices.Contains(worker, tool) || slices.Contains(peer, tool) {
			t.Errorf("%s is read-only or judged by the gate: worker %v peer %v", tool, worker, peer)
		}
	}
}

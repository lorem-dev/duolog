package peer

import (
	"os"
	"slices"
	"testing"
)

func startedName(t *testing.T, r *rig) string {
	t.Helper()
	argv := r.started()
	i := slices.Index(argv, "--name")
	if i < 0 || i+1 >= len(argv) {
		t.Fatalf("the Claude peer started without a name: %q", argv)
	}
	return argv[i+1]
}

// A peer starts under a name that says what it is and whose it is, read for
// the lead's own process.
func TestPeerOpensUnderTheLeadsName(t *testing.T) {
	r := newRig(t, withScreens(claudeIdle("")))
	var gotAgent string
	var gotPID int
	r.useDeps(func(d *Deps) {
		d.LeadName = func(agent string, pid int) string {
			gotAgent, gotPID = agent, pid
			return "Fix the parser (v2)"
		}
	})
	r.open("plan")
	if got, want := startedName(t, r), "duolog-peer-plan (lead: Fix the parser (v2))"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	if gotAgent != "codex" || gotPID != os.Getpid() {
		t.Fatalf("the lead's name was read for %s pid %d, want codex pid %d", gotAgent, gotPID, os.Getpid())
	}
}

// A lead whose name cannot be read still gets its peers.
func TestPeerNameFallsBackToTheLeadID(t *testing.T) {
	for _, read := range []func(string, int) string{nil, func(string, int) string { return "" }} {
		r := newRig(t, withScreens(claudeIdle("")))
		r.useDeps(func(d *Deps) { d.LeadName = read })
		r.open("plan")
		if got, want := startedName(t, r), "duolog-peer-plan (lead: lead-1)"; got != want {
			t.Fatalf("name = %q, want %q", got, want)
		}
	}
}

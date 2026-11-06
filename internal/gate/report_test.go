package gate

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// A peer may write its own report file and nothing else under .duolog.
func TestPeerWritesOnlyItsOwnReport(t *testing.T) {
	p := policy(t)
	own := Action{Kind: "write", Target: ReportPath("peer-abc123"), Role: "implement", Peer: "peer-abc123"}
	for _, m := range []mode.Mode{mode.Ask, mode.Plan, mode.Full} {
		check(t, p, LeadRules{}, m, own, Allow)
		// A read-only role sandbox must not stop the report either.
		check(t, p, LeadRules{RoleSandbox: "read-only"}, m, own, Allow)
	}

	other := own
	other.Peer = "peer-zzz999"
	check(t, p, LeadRules{}, mode.Full, other, Deny)

	noPeer := own
	noPeer.Peer = ""
	check(t, p, LeadRules{}, mode.Full, noPeer, Deny)

	for _, target := range []string{
		".duolog/tasks/peer-abc123/other.md",
		".duolog/tasks/peer-abc123/report.md.bak",
		".duolog/tasks/peer-abc123/sub/report.md",
		".duolog/tasks/peer-zzz999/report.md",
		".duolog/config.yaml",
		".duolog/tasks/report.md",
	} {
		a := own
		a.Target = target
		check(t, p, LeadRules{}, mode.Full, a, Deny)
	}

	// A peer name that is a path or a glob never matches.
	for _, peer := range []string{"..", "peer-a/../x", "*", "peer-{a,b}"} {
		a := Action{Kind: "write", Target: ReportPath(peer), Role: "implement", Peer: peer}
		if d := Evaluate(p, LeadRules{}, mode.Full, a); d.Kind == Allow {
			t.Fatalf("peer %q: %+v", peer, d)
		}
	}

	// The lead's own denies still win.
	lead := LeadRules{Deny: []config.Rule{{Action: "write", Match: ".duolog/**", Decision: Deny}}}
	check(t, p, lead, mode.Full, own, Deny)

	// Reading the report was never blocked, and stays so.
	check(t, p, LeadRules{}, mode.Full, Action{Kind: "read", Target: ReportPath("peer-abc123"), Role: "implement"}, Allow)
}

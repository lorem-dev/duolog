package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

func peerIDAction(t *testing.T, command, role string) Action {
	t.Helper()
	root := t.TempDir()
	return Action{Kind: "exec", Target: command, Role: role, Peer: "peer-x", Work: root, Cwd: root}
}

// Only a blank or a tab separates the words of the print. A newline makes two
// commands of it, printenv alone dumps the environment, and the rule returns
// before the limits of plan and read-only roles.
func TestPeerIDRuleAllowsOnlyTheExactPrint(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		"printenv\nDUOLOG_PEER",
		"echo\n$DUOLOG_PEER",
		"echo\n\"$DUOLOG_PEER\"",
		"printenv\rDUOLOG_PEER",
		"printenv\fDUOLOG_PEER",
		"printenv\vDUOLOG_PEER",
		"printenv DUOLOG_PEER",
		"printenv DUOLOG_PEER\nprintenv",
		"printenv DUOLOG_PEER HOME",
		"printenv",
	} {
		for _, role := range []string{"implement", "review"} {
			for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
				d := Evaluate(p, LeadRules{}, m, peerIDAction(t, command, role))
				if d.Rule == RulePeerID {
					t.Errorf("%s, %s: %q: %+v", m, role, command, d)
				}
				if m == mode.Plan && d.Kind == Allow {
					t.Errorf("plan, %s: %q is allowed: %+v", role, command, d)
				}
			}
		}
	}
}

func TestPeerIDRuleAllowsThePrintInEveryModeAndRole(t *testing.T) {
	p := policy(t)
	for _, command := range []string{
		`printenv DUOLOG_PEER`,
		"printenv \t DUOLOG_PEER",
		`  echo "$DUOLOG_PEER"  `,
		`echo $DUOLOG_PEER`,
		`echo ${DUOLOG_PEER}`,
		`echo "${DUOLOG_PEER}"`,
	} {
		for _, role := range []string{"implement", "review"} {
			for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
				if d := Evaluate(p, LeadRules{}, m, peerIDAction(t, command, role)); d.Kind != Allow || d.Rule != RulePeerID {
					t.Errorf("%s, %s: %q: %+v", m, role, command, d)
				}
			}
		}
	}
}

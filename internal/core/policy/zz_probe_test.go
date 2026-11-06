package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestZZProbe(t *testing.T) {
	p := policy(t)
	lead := LeadRules{RoleSandbox: "read-only"}
	for _, command := range []string{
		`rg -n "a b" internal`,
		`cat "a b"`,
		`rg -n -F X -A45 internal`,
		`rg -n -F "a b" -A45 internal`,
		`rg -n -F 'a b' -A45 internal`,
		`rg -n -F "a(b" -A45 internal`,
		`rg -n -F 'x' -A45 internal`,
		`grep -n X -A14 internal/gate/gate.go | head`,
		`grep -n X -A14 internal/gate/gate.go | head -50`,
		`grep -n "a b" -A14 internal/gate/gate.go | head -50`,
		`grep -n 'a b' -A14 internal/gate/gate.go`,
		`rg -n -F X -A45 internal 2>/dev/null`,
	} {
		for _, shell := range []string{"", "bash"} {
			d := Evaluate(p, lead, mode.Full, Action{Kind: "exec", Target: command, Role: "review_final", Shell: shell})
			t.Logf("%-6s %-55s %s %s %s", shell, command, d.Kind, d.Reason, d.Rule)
		}
	}
}

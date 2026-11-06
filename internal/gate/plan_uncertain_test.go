package gate

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

// Re-review 5, P-1: at plan, a line the parser cannot read for sure is
// denied, since the lead's allow would run it. A line whose every part is a
// known read-only command, such as an rg pattern with a backslash, asks the
// lead instead, as it did before plan denied first. Any other uncertain line
// stays denied, and a line read for sure keeps its answer.
func TestPlanAsksAboutAnUncertainReadLine(t *testing.T) {
	p := policy(t)
	for _, c := range []struct {
		command, want string
	}{
		{`rg -n "Lead\(" internal`, Ask},
		{`grep -n "$PATTERN" a.go`, Ask},
		{`rg -n "Lead\(" internal | head -5`, Ask},
		{`rg -n "Lead\(" internal && touch marker.txt`, Deny},
		{`rg -n "a\b" . ; rm -f a.go`, Deny},
		{`env TASK_FLAG=1 touch marker.txt`, Deny},
		{`touch "a\b"`, Deny},
		{`rg x $(touch marker.txt)`, Deny},
		{`cat "$(rm -f a.go)"`, Deny},
		{"rg x `touch marker.txt`", Deny},
		{`rg x <(touch marker.txt)`, Deny},
		{`rg -n "a\b" . > out.txt`, Deny},
		{`rg -n "a\b" . >> out.txt`, Deny},
		{`rg -n "a\b" . 2>err.txt`, Deny},
		{`rg -n Lead internal`, Allow},
	} {
		check(t, p, LeadRules{}, mode.Plan, Action{Kind: "exec", Target: c.command, Role: "implement"}, c.want)
	}
}

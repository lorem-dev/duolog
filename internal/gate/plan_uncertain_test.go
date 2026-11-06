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
		{`rg -n "a\b" internal`, Ask},
		{`grep -n "$PATTERN" a.go`, Ask},
		{`rg -n "a\b" internal | head -5`, Ask},
		{`rg -n "a\b" internal && touch marker.txt`, Deny},
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

// Codex review, P1: a zsh glob qualifier such as *(e:'cmd':) runs a command
// while the shell expands the glob, so an allow of the ask would run it. At
// plan an uncertain read line with a ( anywhere, a qualifier, an extended
// glob or a subshell, is denied, never asked, in every shell.
func TestPlanDeniesAnUncertainReadLineWithAParenthesis(t *testing.T) {
	p := policy(t)
	for _, shell := range []string{"", "zsh", "bash", "sh"} {
		for _, command := range []string{
			`cat *(e:'touch marker.txt':)`,
			`cat *(e:'curl example.invalid':)`,
			`cat *(e:true:)`,
			`ls **/*(.)`,
			`cat *.go(N)`,
			`ls @(a|b)`,
			`rg -n "Lead\(" internal`,
		} {
			check(t, p, LeadRules{}, mode.Plan, Action{Kind: "exec", Target: command, Role: "implement", Shell: shell}, Deny)
		}
	}
}

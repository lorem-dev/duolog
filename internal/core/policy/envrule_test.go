package policy

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// A variable set in front of a command is allowed only by a rule that names
// it; the rest of the line still meets every deny, the mode and the role.
func TestRuleThatNamesVariablesAllowsThem(t *testing.T) {
	p := policy(t)
	p.Rules = append([]config.Rule{{Action: "exec", Match: "UE_ROOT=* task ue:*", Decision: Allow}}, p.Rules...)
	eval := func(command, role string, m mode.Mode) string {
		return Evaluate(p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: role, Work: t.TempDir()}).Kind
	}
	for _, c := range []struct {
		command, role string
		m             mode.Mode
		want          string
	}{
		{"UE_ROOT=/opt/engine task ue:build", "implement", mode.Edits, Allow},
		{"UE_ROOT=/opt/engine task ue:test -- --filter X", "implement", mode.Full, Allow},
		{"UE_ROOT=/opt/engine task ue:build", "implement", mode.Ask, Ask},
		{"UE_ROOT=/opt/engine task ue:build", "implement", mode.Plan, Deny},
		{"UE_ROOT=/opt/engine task ue:build", "review_cross", mode.Edits, Deny},
		{"X=1 go test ./...", "implement", mode.Edits, Ask},
		{"UE_ROOT=/opt/engine LD_PRELOAD=x task ue:build", "implement", mode.Edits, Ask},
		{"UE_ROOT=/opt/engine task ue:build && touch x", "implement", mode.Edits, Ask},
		{"UE_ROOT=/opt/engine task ue:build > /etc/x", "implement", mode.Edits, Ask},
		{"UE_ROOT=/opt/engine task ue:build; rm -rf /", "implement", mode.Edits, Deny},
		{"UE_ROOT=$(claude) task ue:build", "implement", mode.Edits, Deny},
		{"UE_ROOT=x claude", "implement", mode.Edits, Deny},
		{"UE_ROOT=/x git push", "implement", mode.Edits, Ask},
	} {
		if got := eval(c.command, c.role, c.m); got != c.want {
			t.Errorf("%s %s %q: %s, want %s", c.m, c.role, c.command, got, c.want)
		}
	}
}

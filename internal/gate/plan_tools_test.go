package gate

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// Claude review 3: a Claude worker or peer at level plan, or with a read-only
// role sandbox, starts in Claude's default mode, so the gate is all that keeps
// it read-only. An MCP tool or a tool the gate cannot judge may write, so it
// is denied there, not asked: a lead's allow cannot run it. Only a rule that
// marks an MCP tool read-only, or the default read-only CodeGraph tools, let
// one run.
func TestPlanDeniesToolsTheGateCannotJudge(t *testing.T) {
	p := config.Policy{OnUnmatched: Ask, Rules: []config.Rule{
		{Action: "mcp", Match: "github/*", Decision: Allow},
		{Action: "unknown", Match: "*", Decision: Allow},
	}}
	held := []struct {
		name string
		lead LeadRules
		m    mode.Mode
		role string
	}{
		{"level plan", LeadRules{}, mode.Plan, "implement"},
		{"read-only role under a full lead", LeadRules{RoleSandbox: "read-only"}, mode.Full, "review_final"},
	}
	for _, h := range held {
		for _, a := range []Action{
			{Kind: "mcp", Target: "github/create_pull_request"},
			{Kind: "mcp", Target: "artifacts/publish_version"},
			{Kind: "unknown", Target: "EnterPlanMode"},
			{Kind: "unknown", Target: "CronCreate"},
			{Kind: "unknown", Target: "SendMessage"},
		} {
			a.Role = h.role
			if d := Evaluate(p, h.lead, h.m, a); d.Kind != Deny || d.Reason == "" || d.Rule == "" {
				t.Errorf("%s: %s %s: %+v, want deny", h.name, a.Kind, a.Target, d)
			}
		}
	}
	// A write role at a looser level keeps today's answers: the rule allows,
	// and an unknown tool asks.
	check(t, p, LeadRules{}, mode.Edits, Action{Kind: "mcp", Target: "github/create_pull_request", Role: "implement"}, Allow)
	check(t, p, LeadRules{}, mode.Edits, Action{Kind: "unknown", Target: "CronCreate", Role: "implement"}, Ask)
	// The default CodeGraph read tools still run at plan and in a read-only role.
	def := policy(t)
	check(t, def, LeadRules{}, mode.Plan, Action{Kind: "mcp", Target: "codegraph/codegraph_search", Role: "implement"}, Allow)
	check(t, def, LeadRules{RoleSandbox: "read-only"}, mode.Full, Action{Kind: "mcp", Target: "codegraph/codegraph_explore", Role: "review_final"}, Allow)
	check(t, def, LeadRules{RoleSandbox: "read-only"}, mode.Full, Action{Kind: "mcp", Target: "codegraph/codegraph_init", Role: "review_final"}, Deny)
	// A rule that marks a tool read-only lets it run there; the mark does not
	// widen a tool the rule does not name.
	marked := config.Policy{OnUnmatched: Ask, Rules: []config.Rule{
		{Action: "mcp", Match: "tracker/get_*", Decision: Allow, ReadOnly: true},
	}}
	check(t, marked, LeadRules{}, mode.Plan, Action{Kind: "mcp", Target: "tracker/get_issue", Role: "implement"}, Allow)
	check(t, marked, LeadRules{RoleSandbox: "read-only"}, mode.Ask, Action{Kind: "mcp", Target: "tracker/get_issue", Role: "review_final"}, Allow)
	check(t, marked, LeadRules{RoleSandbox: "read-only"}, mode.Ask, Action{Kind: "mcp", Target: "tracker/create_issue", Role: "review_final"}, Deny)
	// The mark belongs on an MCP allow only; anywhere else the policy is invalid.
	bad := config.Policy{OnUnmatched: Ask, Rules: []config.Rule{{Action: "write", Match: "**", Decision: Allow, ReadOnly: true}}}
	check(t, bad, LeadRules{}, mode.Edits, Action{Kind: "read", Target: "a.go", Role: "implement"}, Deny)
}

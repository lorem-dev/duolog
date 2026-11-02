package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

// The hook inputs below have the shape each agent sent for an MCP call in the
// live check (ids removed): Codex writes - in a server name as _.
func TestNormalizeMCPCalls(t *testing.T) {
	cases := []struct{ agent, input, kind, target string }{
		{"codex", `{"hook_event_name":"PreToolUse","permission_mode":"bypassPermissions","tool_name":"mcp__mcp_jira__jira_get_issue","tool_input":{"issue_key":"ABC-1","fields":"summary,status"}}`, "mcp", "mcp_jira/jira_get_issue"},
		{"claude", `{"hook_event_name":"PreToolUse","permission_mode":"plan","tool_name":"mcp__confluence__confluence_search","tool_input":{"query":"x","limit":1}}`, "mcp", "confluence/confluence_search"},
		{"claude", `{"hook_event_name":"PreToolUse","tool_name":"mcp__plugin_kit_docs__read","tool_input":{}}`, "mcp", "plugin_kit_docs/read"},
		// Claude sends the configured server name too, which may hold __.
		{"claude", `{"hook_event_name":"PreToolUse","tool_name":"mcp__my__wiki__read_page","tool_input":{},"mcp_server":{"name":"my__wiki","source":"dynamic"}}`, "mcp", "my__wiki/read_page"},
		// ToolSearch loads a deferred tool's definition before Claude calls it.
		{"claude", `{"hook_event_name":"PreToolUse","tool_name":"ToolSearch","tool_input":{"query":"select:mcp__confluence__confluence_search","max_results":1}}`, "internal", "ToolSearch"},
		{"codex", `{"tool_name":"mcp__broken","tool_input":{}}`, "unknown", "mcp__broken"},
	}
	for _, c := range cases {
		a, err := Normalize(c.agent, []byte(c.input))
		if err != nil || a.Kind != c.kind || a.Target != c.target {
			t.Fatalf("%s %s: %+v %v", c.agent, c.input, a, err)
		}
	}
}

func TestMCPRules(t *testing.T) {
	p := config.Policy{OnUnmatched: Ask, Rules: []config.Rule{
		{Action: "mcp", Match: "jira/delete_*", Decision: Deny},
		{Action: "mcp", Match: "mcp-jira/jira_get_*", Decision: Allow},
		{Action: "mcp", Match: "wiki/read", Decision: Allow, Roles: []string{"implement"}},
		{Action: "unknown", Match: "*", Decision: Allow},
	}}
	for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
		// A listed tool runs in every mode: the person named it.
		check(t, p, LeadRules{RoleSandbox: "read-only"}, m, Action{Kind: "mcp", Target: "mcp_jira/jira_get_issue", Role: "review_final"}, Allow)
		// An unlisted tool asks, in full mode too, and an unknown-tool allow
		// does not cover it.
		check(t, p, LeadRules{}, m, Action{Kind: "mcp", Target: "mcp_jira/jira_create_issue", Role: "implement"}, Ask)
		check(t, p, LeadRules{}, m, Action{Kind: "mcp", Target: "jira/delete_issue", Role: "implement"}, Deny)
	}
	check(t, p, LeadRules{}, mode.Edits, Action{Kind: "mcp", Target: "wiki/read", Role: "implement"}, Allow)
	check(t, p, LeadRules{}, mode.Edits, Action{Kind: "mcp", Target: "wiki/read", Role: "review_final"}, Ask)
	// The lead's own deny wins over a project allow.
	lead := LeadRules{Deny: []config.Rule{{Action: "mcp", Match: "mcp_jira/*", Decision: Deny}}}
	check(t, p, lead, mode.Full, Action{Kind: "mcp", Target: "mcp_jira/jira_get_issue", Role: "implement"}, Deny)
	p.OnUnmatched = Deny
	check(t, p, LeadRules{}, mode.Edits, Action{Kind: "mcp", Target: "other/x", Role: "implement"}, Deny)
	bad := config.Policy{OnUnmatched: Ask, Rules: []config.Rule{{Action: "mcp", Match: "jira", Decision: Allow}}}
	check(t, bad, LeadRules{}, mode.Edits, Action{Kind: "mcp", Target: "jira/x"}, Deny)
}

func TestLeadMCPDenies(t *testing.T) {
	root := t.TempDir()
	policy(t)
	path := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"permissions":{"deny":["mcp__jira__create_issue","mcp__wiki"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	lead, err := ImportLead(root, "claude")
	if err != nil {
		t.Fatal(err)
	}
	p := config.Policy{OnUnmatched: Ask, Rules: []config.Rule{{Action: "mcp", Match: "*/*", Decision: Allow}}}
	check(t, p, lead, mode.Full, Action{Kind: "mcp", Target: "jira/create_issue"}, Deny)
	check(t, p, lead, mode.Full, Action{Kind: "mcp", Target: "wiki/anything"}, Deny)
	check(t, p, lead, mode.Full, Action{Kind: "mcp", Target: "jira/get_issue"}, Allow)
}

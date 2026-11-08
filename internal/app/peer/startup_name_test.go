package peer

import (
	"strings"
	"testing"
)

// When open waits on a startup screen it cannot answer, the question names the
// screen, so the lead reads what to look at.
func TestOpenNamesTheStartupScreenItWaitsOn(t *testing.T) {
	mcp := "New MCP servers found in .mcp.json\n\nSelect any you wish to enable:\n\n❯ 1. Use this and all future MCP servers in this project\n  2. Use this MCP server\n  3. Continue without using this MCP server\n"
	for _, tc := range []struct{ name, screen, want string }{
		{"folder trust", fixtureText("claude-trust-folder.txt"), "folder trust"},
		{"new MCP servers", mcp, "new MCP servers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, withScreens(tc.screen))
			r.cfg.Rex.AutoAnswers = nil
			e := r.open("plan")
			if e.State != Question || !strings.HasPrefix(e.Question, tc.want+": ") {
				t.Fatalf("%+v; want a question that starts with %q", e, tc.want)
			}
		})
	}
}

package screen

import (
	"strings"
	"testing"
)

func TestStartupNameRecognisesTheScreensAPeerStopsAt(t *testing.T) {
	mcp := "New MCP servers found in .mcp.json\n\nSelect any you wish to enable:\n\n❯ 1. Use this and all future MCP servers in this project\n  2. Use this MCP server\n  3. Continue without using this MCP server\n"
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{"claude folder trust", []byte(fixture(t, "claude-trust-folder.txt")), "folder trust"},
		{"codex folder trust", []byte(fixture(t, "codex-trust-folder.txt")), "folder trust"},
		{"claude mcp", []byte(mcp), "new MCP servers"},
		{"single server", []byte("New MCP server found in .mcp.json: alpha\n\n❯ 1. Use this MCP server\n  2. Continue without using this MCP server\n"), "new MCP servers"},
		{"other question", []byte("Do you want to proceed?\n\n❯ 1. Yes\n  2. No\n"), ""},
	}
	for _, c := range cases {
		agentName := "claude"
		if strings.HasPrefix(c.name, "codex") {
			agentName = "codex"
		}
		if got := StartupName(parseAs(agentName, c.raw)); got != c.want {
			t.Errorf("%s: StartupName = %q, want %q", c.name, got, c.want)
		}
	}
}

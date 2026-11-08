package claude

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// PendingProjectServers lists the servers of the project's .mcp.json, or of a
// parent folder's, that Claude has neither enabled nor disabled for the folder:
// Claude stops at its "new MCP servers found" dialog for them.
func TestPendingProjectServers(t *testing.T) {
	servers := `{"mcpServers":{"a":{"command":"x"},"b":{"command":"y"}}}`
	state := func(home, root, body string) {
		putFile(t, filepath.Join(home, ".claude.json"), `{"projects":{"`+filepath.ToSlash(root)+`":`+body+`}}`)
	}
	cases := []struct {
		name  string
		setup func(home, root string)
		want  []string
	}{
		{"none decided", func(home, root string) {
			putFile(t, filepath.Join(root, ".mcp.json"), servers)
		}, []string{"a", "b"}},
		{"one enabled", func(home, root string) {
			putFile(t, filepath.Join(root, ".mcp.json"), servers)
			state(home, root, `{"enabledMcpjsonServers":["a"]}`)
		}, []string{"b"}},
		{"one disabled", func(home, root string) {
			putFile(t, filepath.Join(root, ".mcp.json"), servers)
			state(home, root, `{"disabledMcpjsonServers":["b"],"enabledMcpjsonServers":["a"]}`)
		}, nil},
		{"enable all", func(home, root string) {
			putFile(t, filepath.Join(root, ".mcp.json"), servers)
			state(home, root, `{"enableAllProjectMcpServers":true}`)
		}, nil},
		{"user settings enable all", func(home, root string) {
			putFile(t, filepath.Join(root, ".mcp.json"), servers)
			putFile(t, filepath.Join(home, ".claude", "settings.json"), `{"enableAllProjectMcpServers":true}`)
		}, nil},
		{"parent folder", func(home, root string) {
			putFile(t, filepath.Join(filepath.Dir(root), ".mcp.json"), `{"mcpServers":{"p":{"command":"z"}}}`)
		}, []string{"p"}},
		{"no file", func(home, root string) {}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cp, home, base := capsEnv(t)
			root := filepath.Join(base, "work")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			c.setup(home, root)
			got := cp.PendingProjectServers(root, os.Environ())
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Fatalf("pending = %v, want %v", got, c.want)
			}
		})
	}
}

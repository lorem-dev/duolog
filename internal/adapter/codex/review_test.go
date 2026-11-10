package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDisabledGateIsNotTrusted(t *testing.T) {
	// Mutation killed: ignoring enabled=false admits a hook Codex will not run.
	root := t.TempDir()
	key := filepath.Join(root, ".codex", "hooks.json") + ":pre_tool_use:0:0"
	trustFixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture\"\nenabled = false\n", key))
	writeHook(t, root, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`)
	got, err := hooks(root)
	if err != nil || got.Trusted {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestServersIncludeTrustedAncestorLayers(t *testing.T) {
	// Mutation killed: merging only the immediate project omits ancestor MCP servers.
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	trustFixture(t, fmt.Sprintf("[projects.%q]\ntrust_level = 'trusted'\n", root))
	writeFile(t, filepath.Join(parent, ".codex", "config.toml"), "[mcp_servers.ancestor]\ncommand = 'ancestor-server'\n")
	writeFile(t, filepath.Join(root, ".codex", "config.toml"), "[mcp_servers.local]\ncommand = 'local-server'\n")
	defs := (Caps{}).Servers(root, os.Environ())
	if _, ok := defs["ancestor"]; !ok {
		t.Fatalf("ancestor missing: %v", defs)
	}
}

package delegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/mode"
)

const leadToken = "s3cret-lead-token"

// bridgeHarness has a Claude lead whose ~/.claude.json defines jira with a
// token header and wiki with OAuth, and a Codex worker that has neither.
func bridgeHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	h.cfg.Capabilities.OnMissing.MCP = capability.ActionBridge
	home := os.Getenv("HOME")
	put(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"jira":{"type":"http","url":"https://j.example.com/mcp","headers":{"X-Token":"`+leadToken+`"}},"wiki":{"type":"http","url":"https://w.example.com/mcp","oauth":{"clientId":"c"}}}}`)
	h.inventories(capability.Inventory{Agent: "codex"}, capability.Inventory{Agent: "claude", Items: []capability.Item{
		{Kind: "mcp", Name: "jira", Bridgeable: true},
		{Kind: "mcp", Name: "wiki", NoBridge: "it signs in with OAuth"},
	}})
	return h
}

func TestBridgedServerReachesWorker(t *testing.T) {
	h := bridgeHarness(t)
	// The worker's home, whose Codex config the bridge reads: HOME, or
	// USERPROFILE on Windows.
	h.cfg.EnvPassthrough = []string{"HOME", "USERPROFILE"}
	h.cfg.Capabilities.MCPTools = map[string][]string{"jira": {"get_issue"}}
	o, e := h.runNeeds("implement", "mcp:jira")
	h.requireCalls(1)
	if e != nil || !strings.Contains(o.Line, "bridged mcp:jira from claude") {
		t.Fatal(o, e)
	}
	spec := h.calls[0]
	if spec.MCP == nil || strings.Join(spec.MCP.Names, ",") != "jira" || spec.KeepMCP {
		t.Fatalf("%+v", spec.MCP)
	}
	args := strings.Join(spec.MCP.CodexArgs, " ")
	if !strings.Contains(args, `mcp_servers.jira.enabled_tools=["get_issue"]`) || strings.Contains(args, "wiki") || strings.Contains(args, leadToken) {
		t.Fatal(args)
	}
	if !strings.Contains(strings.Join(spec.MCP.Env, " "), leadToken) {
		t.Fatal("the worker env lacks the value")
	}
	// The bus, the task copy and every other file stay free of the value.
	_ = filepath.Walk(h.s.Dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(read(t, path), leadToken) {
			t.Fatalf("token in %s", path)
		}
		return nil
	})
}

func TestUnbridgeableServerGoesToLead(t *testing.T) {
	h := bridgeHarness(t)
	o, e := h.runNeeds("implement", "mcp:wiki")
	h.requireCalls(0)
	if e != nil || len(o.Needs) != 1 || o.Needs[0] != "mcp:wiki" || !strings.Contains(o.Line, "cannot be bridged: it signs in with OAuth") {
		t.Fatal(o, e)
	}
}

// A failure found only when translating, such as a missing variable, also
// goes to the lead with the reason.
func TestBridgeFailureGoesToLead(t *testing.T) {
	h := bridgeHarness(t)
	put(t, filepath.Join(os.Getenv("HOME"), ".claude.json"), `{"mcpServers":{"jira":{"type":"http","url":"https://j.example.com/mcp","headers":{"X-Token":"${DUOLOG_TEST_NOT_SET}"}}}}`)
	o, e := h.runNeeds("implement", "mcp:jira")
	h.requireCalls(0)
	if e != nil || len(o.Needs) != 1 || !strings.Contains(o.Line, "DUOLOG_TEST_NOT_SET, which the lead does not have") {
		t.Fatal(o, e)
	}
}

// A worker that also needs its own server keeps its servers next to the
// bridged one.
func TestBridgedAndOwnServers(t *testing.T) {
	h := bridgeHarness(t)
	h.inventories(capability.Inventory{Agent: "codex", Items: []capability.Item{{Kind: "mcp", Name: "mine"}}}, capability.Inventory{Agent: "claude", Items: []capability.Item{{Kind: "mcp", Name: "jira", Bridgeable: true}}})
	if _, e := h.runNeeds("implement", "mcp:jira", "mcp:mine"); e != nil {
		t.Fatal(e)
	}
	h.requireCalls(1)
	if !h.calls[0].KeepMCP || h.calls[0].MCP == nil {
		t.Fatalf("%+v", h.calls[0])
	}
}

// lead_first stays the default: nothing is bridged unless the user turns it on.
func TestBridgeIsOffByDefault(t *testing.T) {
	h := bridgeHarness(t)
	h.cfg.Capabilities.OnMissing.MCP = capability.ActionLeadFirst
	o, e := h.runNeeds("implement", "mcp:jira")
	h.requireCalls(0)
	if e != nil || len(o.Needs) != 1 || strings.Contains(o.Line, "bridge") {
		t.Fatal(o, e)
	}
}

// mcp_tools also limits a server the worker has itself.
func TestOwnServerLimitedByToolList(t *testing.T) {
	h := bridgeHarness(t)
	h.cfg.Capabilities.MCPTools = map[string][]string{"mine": {"read"}}
	h.inventories(capability.Inventory{Agent: "codex", Items: []capability.Item{{Kind: "mcp", Name: "mine"}}}, capability.Inventory{Agent: "claude"})
	// A server Codex does not define, such as a plugin's, gets no enabled_tools:
	// the table would have no transport.
	if _, e := h.runNeeds("implement", "mcp:mine"); e != nil {
		t.Fatal(e)
	}
	h.requireCalls(1)
	if m := h.calls[0].MCP; m != nil {
		t.Fatalf("%+v", m)
	}
	config := filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")
	old := read(t, config)
	put(t, config, old+"\n[mcp_servers.mine]\ncommand = \"mine\"\n")
	if _, e := h.runNeeds("implement", "mcp:mine"); e != nil {
		t.Fatal(e)
	}
	h.requireCalls(2)
	m := h.calls[1].MCP
	if m == nil || len(m.Names) != 0 || strings.Join(m.CodexArgs, " ") != `-c mcp_servers.mine.enabled_tools=["read"]` {
		t.Fatalf("%+v", m)
	}
}

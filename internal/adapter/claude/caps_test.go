package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func putFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
}

func capsFixture(t *testing.T, name string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "scripts", "bench", "fixtures", "capabilities", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// capsEnv gives Caps with an empty home and a project root. Callers narrow
// PATH with capsPath after installing fakes, because installing needs the go
// tool.
func capsEnv(t *testing.T) (c Caps, home, root string) {
	t.Helper()
	home, root = t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return Caps{Home: home}, home, root
}

func capsPath(t *testing.T, bins ...string) {
	t.Helper()
	dirs := []string{t.TempDir()}
	for _, b := range bins {
		dirs = append(dirs, filepath.Dir(b))
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

func itemNames(inv caps.Inventory) []string {
	var out []string
	for _, it := range inv.Items {
		out = append(out, it.Kind+":"+it.Name)
	}
	return out
}

// claudeState writes ~/.claude.json with user servers and, when local is set,
// the project's local servers.
func claudeState(t *testing.T, home, root, user, local string) {
	t.Helper()
	projects := "{}"
	if local != "" {
		data, _ := json.Marshal(map[string]any{root: json.RawMessage(`{"mcpServers":` + local + `}`)})
		projects = string(data)
	}
	putFile(t, filepath.Join(home, ".claude.json"), `{"mcpServers":`+user+`,"projects":`+projects+`}`)
}

// The scan reads the skills under Caps.Home and never the home folder of the
// environment: a test sets the field, not a package variable.
func TestCapsHomeIsAField(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	real := t.TempDir()
	t.Setenv("HOME", real)
	t.Setenv("USERPROFILE", real)
	putFile(t, filepath.Join(real, ".claude", "skills", "from-env-home", "SKILL.md"), "x")
	dir := t.TempDir()
	putFile(t, filepath.Join(dir, ".claude", "skills", "from-field", "SKILL.md"), "x")
	capsPath(t, testbin.Install(t, "claude", testbin.Scenario{Stdout: "[]"}))
	inv, err := Caps{Home: dir}.Scan(context.Background(), t.TempDir(), os.Environ())
	if err != nil || inv.Unknown {
		t.Fatal(inv, err)
	}
	got := strings.Join(itemNames(inv), " ")
	if !strings.Contains(got, "skill:from-field") || strings.Contains(got, "from-env-home") {
		t.Fatalf("items = %s; want the skills under Caps.Home only", got)
	}
}

func TestClaudePrecedence(t *testing.T) {
	cases := []struct {
		name, user, project, local, settings, want string
	}{
		{"local wins", `{"s":{"command":"user"}}`, `{"s":{"command":"project"}}`, `{"s":{"command":"local"}}`, `{"enableAllProjectMcpServers":true}`, "local"},
		{"approved project wins over user", `{"s":{"command":"user"}}`, `{"s":{"command":"project"}}`, "", `{"enabledMcpjsonServers":["s"]}`, "project"},
		{"unapproved project does not hide user", `{"s":{"command":"user"}}`, `{"s":{"command":"project"}}`, "", `{}`, "user"},
		{"rejected project does not hide user", `{"s":{"command":"user"}}`, `{"s":{"command":"project"}}`, "", `{"enableAllProjectMcpServers":true,"disabledMcpjsonServers":["s"]}`, "user"},
		{"user alone", `{"s":{"command":"user"}}`, "", "", `{}`, "user"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cp, home, root := capsEnv(t)
			claudeState(t, home, root, c.user, c.local)
			if c.project != "" {
				putFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":`+c.project+`}`)
			}
			putFile(t, filepath.Join(home, ".claude", "settings.json"), c.settings)
			s := cp.Servers(root, os.Environ())["s"]
			if s.Command != c.want || s.Scope != c.want || s.Why != "" {
				t.Fatalf("%+v", s)
			}
		})
	}
}

// An unapproved project server alone is listed but never bridged.
func TestUnapprovedProjectServerIsNotBridged(t *testing.T) {
	cp, _, root := capsEnv(t)
	putFile(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"s":{"command":"evil"}}}`)
	// The committed settings file counts only in a folder Claude trusts.
	putFile(t, filepath.Join(root, ".claude", "settings.json"), `{"enableAllProjectMcpServers":true}`)
	if s := cp.Servers(root, os.Environ())["s"]; !strings.Contains(s.Why, "not approved") {
		t.Fatalf("%+v", s)
	}
}

// The scan lists the servers and its cache holds them, with no value of their
// config. The marks come from the other agent's Refusal; the test of the
// marked inventory is TestScanMarksBridgeableWithoutSecrets in app/delegate,
// which may import both adapters.
func TestScanCachesServersWithoutSecrets(t *testing.T) {
	const secret = "s3cret-alpha-value"
	cp, home, root := capsEnv(t)
	capsPath(t, testbin.Install(t, "claude", testbin.Scenario{}))
	putFile(t, filepath.Join(root, ".duolog", ".gitignore"), "*\n")
	claudeState(t, home, root, `{"jira":{"type":"http","url":"https://j.example.com","headers":{"X-Token":"`+secret+`"}},"notion":{"type":"http","url":"https://n.example.com"}}`, "")
	if _, err := cp.Scan(context.Background(), root, os.Environ()); err != nil {
		t.Fatal(err)
	}
	cached, _ := os.ReadFile(filepath.Join(root, ".duolog", "cache", "inventory-claude.json"))
	if got := strings.Join(itemNames(cp.Cached(root)), " "); got != "mcp:jira mcp:notion" {
		t.Fatalf("cached items = %s; want both servers", got)
	}
	if strings.Contains(string(cached), secret) || strings.Contains(string(cached), "X-Token") {
		t.Fatal(string(cached))
	}
}

func TestScanMarksIncompleteKindsUnknown(t *testing.T) {
	cp, _, root := capsEnv(t)
	capsPath(t, testbin.Install(t, "claude", testbin.Scenario{}))
	inv, err := cp.Scan(context.Background(), root, os.Environ())
	if err != nil || inv.Unknown || !inv.KindUnknown(caps.KindMCP) || inv.KindUnknown(caps.KindSkill) {
		t.Fatal(inv, err)
	}
}

func TestClaudePluginsFromRealList(t *testing.T) {
	cp, _, root := capsEnv(t)
	capsPath(t, testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: capsFixture(t, "claude-plugin-list.json")}}}))
	inv, err := cp.Scan(context.Background(), root, os.Environ())
	if err != nil || inv.Unknown || inv.KindUnknown(caps.KindPlugin) || inv.KindUnknown(caps.KindSkill) {
		t.Fatal(inv, err)
	}
	got := strings.Join(itemNames(inv), " ")
	if !strings.Contains(got, "plugin:clangd-lsp") || !strings.Contains(got, "plugin:crit") {
		t.Fatal(got)
	}
}

func TestClaudeCapabilityConfigOverride(t *testing.T) {
	cp, home, root := capsEnv(t)
	custom := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	putFile(t, filepath.Join(home, ".claude", "skills", "ignored", "SKILL.md"), "ignored")
	putFile(t, filepath.Join(custom, "skills", "visible", "SKILL.md"), "visible")
	putFile(t, filepath.Join(custom, ".claude.json"), `{"mcpServers":{"override-server":{}}}`)
	capsPath(t, testbin.Install(t, "claude", testbin.Scenario{Stdout: "[]"}))
	inv, err := cp.Scan(context.Background(), root, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(itemNames(inv), "\n")
	if strings.Contains(got, "ignored") || !strings.Contains(got, "visible") || !strings.Contains(got, "override-server") {
		t.Fatal(got)
	}
}

const duologServerJSON = `{"type":"stdio","command":"duolog","args":["mcp"]}`

// The duolog server in the user's config is listed, marked lead only, and
// never reaches a worker, whatever name it has.
func TestBridgeNeverCarriesDuologServer(t *testing.T) {
	cp, home, root := capsEnv(t)
	claudeState(t, home, root, `{"duolog":`+duologServerJSON+`,"renamed":{"command":"/opt/bin/duolog.exe","args":["mcp"]},"svc":{"command":"svc-bin","args":["serve"]}}`, "")
	servers := cp.Servers(root, os.Environ())
	for _, name := range []string{"duolog", "renamed"} {
		if why := servers[name].Why; !strings.Contains(why, "lead only") {
			t.Fatalf("%s: Why = %q", name, why)
		}
	}
	if servers["svc"].Why != "" {
		t.Fatalf("svc: %+v", servers["svc"])
	}
	// The servers of a lead go to a worker of another agent.
	for name, s := range servers {
		s.Agent = "codex"
		servers[name] = s
	}
	m, failed := cp.Import(root, servers, nil, nil)
	if m == nil || strings.Join(m.Names, ",") != "svc" {
		t.Fatalf("bridged = %+v", m)
	}
	if strings.Contains(string(m.ClaudeConfig), "duolog") {
		t.Fatalf("config = %s", m.ClaudeConfig)
	}
	for _, name := range []string{"duolog", "renamed"} {
		if !strings.Contains(failed[name], "lead only") {
			t.Fatalf("failed[%s] = %q", name, failed[name])
		}
	}
}

// A server of another agent that runs duolog mcp is refused the same way.
func TestRefusalNamesDuologServerLeadOnly(t *testing.T) {
	cp, _, root := capsEnv(t)
	s := caps.Server{Name: "x", Agent: "codex", Transport: "stdio", Command: "/usr/local/bin/duolog", Args: []string{"mcp"}}
	if why := cp.Refusal(root, s, nil); !strings.Contains(why, "lead only") {
		t.Fatalf("Refusal = %q", why)
	}
	s.Args = []string{"status"}
	if why := cp.Refusal(root, s, nil); why != "" {
		t.Fatalf("a duolog command that is not the server: %q", why)
	}
}

// A worker that does not keep the user's servers starts strict, and the
// config it is handed has no duolog entry.
func TestStrictWorkerArgvHasNoDuologServer(t *testing.T) {
	cp, home, root := capsEnv(t)
	claudeState(t, home, root, `{"duolog":`+duologServerJSON+`,"svc":{"command":"svc-bin"}}`, "")
	servers := cp.Servers(root, os.Environ())
	for name, s := range servers {
		s.Agent = "codex"
		servers[name] = s
	}
	m, _ := cp.Import(root, servers, nil, nil)
	if m == nil {
		t.Fatal("svc was not bridged")
	}
	m.ConfigPath = filepath.Join(t.TempDir(), "mcp.json")
	args := claudeArgv(run.Spec{MCP: m, Process: &launch.Process{}})
	if !slices.Contains(args, "--strict-mcp-config") {
		t.Fatalf("argv = %v", args)
	}
	if strings.Contains(strings.Join(args, " "), "duolog") || strings.Contains(string(m.ClaudeConfig), `"duolog"`) {
		t.Fatalf("argv = %v, config = %s", args, m.ClaudeConfig)
	}
}

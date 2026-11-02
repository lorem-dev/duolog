package capability

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// The secret values below must never appear in argv or in a written file.
const (
	secretA = "s3cret-alpha-value"
	secretB = "s3cret-bravo-value"
	secretC = "s3cret-charlie-value"
)

// leadEnv replaces the lead's environment for one test.
func leadEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	old := lookupEnv
	lookupEnv = func(name string) (string, bool) { v, ok := vars[name]; return v, ok }
	t.Cleanup(func() { lookupEnv = old })
}

func claudeState(t *testing.T, home, root, user, local string) {
	t.Helper()
	projects := "{}"
	if local != "" {
		data, _ := json.Marshal(map[string]any{root: json.RawMessage(`{"mcpServers":` + local + `}`)})
		projects = string(data)
	}
	put(t, filepath.Join(home, ".claude.json"), `{"mcpServers":`+user+`,"projects":`+projects+`}`)
}

// envMap reads NAME=value pairs.
func envMap(pairs []string) map[string]string {
	out := map[string]string{}
	for _, kv := range pairs {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// noSecret fails when a secret is in text.
func noSecret(t *testing.T, where, text string) {
	t.Helper()
	for _, s := range []string{secretA, secretB, secretC} {
		if strings.Contains(text, s) {
			t.Fatalf("%s holds a secret: %s", where, text)
		}
	}
}

// codexValue returns the value of -c key=value in args.
func codexValue(args []string, key string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" {
			if v, ok := strings.CutPrefix(args[i+1], key+"="); ok {
				return v, true
			}
		}
	}
	return "", false
}

func TestBridgeClaudeToCodex(t *testing.T) {
	cases := []struct {
		name, def string
		lead      map[string]string
		want      map[string]string // -c key: value
		env       map[string]string // worker env name or pattern: value
	}{
		{
			name: "stdio",
			def:  `{"command":"docs-server","args":["--stdio"],"env":{"DOCS_TOKEN":"` + secretA + `","DOCS_KEY":"${LEAD_KEY}","DOCS_MODE":"${UNSET:-fast}"}}`,
			lead: map[string]string{"LEAD_KEY": secretB},
			want: map[string]string{
				"mcp_servers.svc.command":  `"docs-server"`,
				"mcp_servers.svc.args":     `["--stdio"]`,
				"mcp_servers.svc.env_vars": `["DOCS_KEY","DOCS_MODE","DOCS_TOKEN"]`,
			},
			env: map[string]string{"DOCS_TOKEN": secretA, "DOCS_KEY": secretB, "DOCS_MODE": "fast"},
		},
		{
			name: "http",
			def:  `{"type":"http","url":"https://mcp.example.com/mcp","headers":{"X-Token":"` + secretA + `","Authorization":"Bearer ${LEAD_TOKEN}"}}`,
			lead: map[string]string{"LEAD_TOKEN": secretB},
			want: map[string]string{
				"mcp_servers.svc.url":              `"https://mcp.example.com/mcp"`,
				"mcp_servers.svc.env_http_headers": `{"Authorization"="DUOLOG_MCP_SVC_1","X-Token"="DUOLOG_MCP_SVC_2"}`,
			},
			env: map[string]string{"DUOLOG_MCP_SVC_1": "Bearer " + secretB, "DUOLOG_MCP_SVC_2": secretA},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, root := env(t)
			leadEnv(t, c.lead)
			claudeState(t, home, root, `{"svc":`+c.def+`}`, "")
			got, failed := Bridge("claude", "codex", root, []string{"svc"}, nil, []string{"PATH=/bin"})
			if got == nil || len(failed) != 0 {
				t.Fatal(got, failed)
			}
			for key, want := range c.want {
				if v, ok := codexValue(got.CodexArgs, key); !ok || v != want {
					t.Errorf("%s = %q, want %q (args %q)", key, v, want, got.CodexArgs)
				}
			}
			workerEnv := envMap(got.Env)
			for k, v := range c.env {
				if workerEnv[k] != v {
					t.Errorf("worker env %s = %q, want %q", k, workerEnv[k], v)
				}
			}
			noSecret(t, "argv", strings.Join(got.CodexArgs, " "))
			// The model's shell must not see the values.
			if v, ok := codexValue(got.CodexArgs, "shell_environment_policy.filters.DUOLOG_MCP_*"); !ok || v != `"exclude"` {
				t.Fatal(got.CodexArgs)
			}
			for k := range c.env {
				if _, ok := codexValue(got.CodexArgs, "shell_environment_policy.filters."+k); !ok && !strings.HasPrefix(k, "DUOLOG_MCP_") {
					t.Errorf("%s is not kept from the shell: %q", k, got.CodexArgs)
				}
			}
		})
	}
}

func TestBridgeCodexToClaude(t *testing.T) {
	cases := []struct {
		name, def string
		lead      map[string]string
		check     func(t *testing.T, def map[string]any, env map[string]string)
	}{
		{
			name: "stdio",
			def:  "command = \"docs-server\"\nargs = [\"--stdio\"]\nenv_vars = [\"LEAD_KEY\", \"NOT_SET\"]\n[mcp_servers.svc.env]\nDOCS_TOKEN = \"" + secretA + "\"\n",
			lead: map[string]string{"LEAD_KEY": secretB},
			check: func(t *testing.T, def map[string]any, env map[string]string) {
				if def["type"] != "stdio" || def["command"] != "docs-server" {
					t.Fatal(def)
				}
				vars := def["env"].(map[string]any)
				if len(vars) != 2 {
					t.Fatal(vars)
				}
				for name, want := range map[string]string{"DOCS_TOKEN": secretA, "LEAD_KEY": secretB} {
					ref, _ := vars[name].(string)
					v := strings.TrimSuffix(strings.TrimPrefix(ref, "${"), "}")
					if env[v] != want {
						t.Errorf("%s: %q -> %q", name, ref, env[v])
					}
				}
			},
		},
		{
			name: "http",
			def:  "url = \"https://mcp.example.com/mcp\"\nbearer_token_env_var = \"LEAD_TOKEN\"\n[mcp_servers.svc.http_headers]\nX-Static = \"" + secretA + "\"\n[mcp_servers.svc.env_http_headers]\nX-From-Env = \"LEAD_HEADER\"\nX-Missing = \"NOT_SET\"\n",
			lead: map[string]string{"LEAD_TOKEN": secretB, "LEAD_HEADER": secretC},
			check: func(t *testing.T, def map[string]any, env map[string]string) {
				if def["type"] != "http" || def["url"] != "https://mcp.example.com/mcp" {
					t.Fatal(def)
				}
				headers := def["headers"].(map[string]any)
				if _, ok := headers["X-Missing"]; ok || len(headers) != 3 {
					t.Fatal(headers)
				}
				for name, want := range map[string]string{"X-Static": secretA, "X-From-Env": secretC, "Authorization": "Bearer " + secretB} {
					ref, _ := headers[name].(string)
					prefix, rest, _ := strings.Cut(ref, "${")
					v := strings.TrimSuffix(rest, "}")
					if prefix+env[v] != want {
						t.Errorf("%s: %q", name, ref)
					}
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, root := env(t)
			leadEnv(t, c.lead)
			put(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.svc]\n"+c.def)
			got, failed := Bridge("codex", "claude", root, []string{"svc"}, nil, nil)
			if got == nil || len(failed) != 0 {
				t.Fatal(got, failed)
			}
			noSecret(t, "config", string(got.ClaudeConfig))
			var doc struct {
				MCP map[string]map[string]any `json:"mcpServers"`
			}
			if err := json.Unmarshal(got.ClaudeConfig, &doc); err != nil || len(doc.MCP) != 1 {
				t.Fatal(string(got.ClaudeConfig), err)
			}
			c.check(t, doc.MCP["svc"], envMap(got.Env))
		})
	}
}

// Only the servers the task names are bridged, never all of them.
func TestBridgeOnlyNamedServers(t *testing.T) {
	home, root := env(t)
	leadEnv(t, nil)
	claudeState(t, home, root, `{"a":{"command":"a"},"b":{"command":"b"}}`, "")
	got, _ := Bridge("claude", "codex", root, []string{"a"}, nil, nil)
	if got == nil || strings.Contains(strings.Join(got.CodexArgs, " "), "mcp_servers.b.") || !slices.Equal(got.Names, []string{"a"}) {
		t.Fatal(got)
	}
	got, _ = Bridge("codex", "claude", root, []string{"a"}, nil, nil)
	if got != nil {
		t.Fatal("codex defines no servers here", got)
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
			home, root := env(t)
			claudeState(t, home, root, c.user, c.local)
			if c.project != "" {
				put(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":`+c.project+`}`)
			}
			put(t, filepath.Join(home, ".claude", "settings.json"), c.settings)
			s := Servers("claude", root)["s"]
			if s.Command != c.want || s.Scope != c.want || s.Why != "" {
				t.Fatalf("%+v", s)
			}
		})
	}
}

// An unapproved project server alone is listed but never bridged.
func TestUnapprovedProjectServerIsNotBridged(t *testing.T) {
	_, root := env(t)
	put(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"s":{"command":"evil"}}}`)
	// The committed settings file counts only in a folder Claude trusts.
	put(t, filepath.Join(root, ".claude", "settings.json"), `{"enableAllProjectMcpServers":true}`)
	if s := Servers("claude", root)["s"]; !strings.Contains(s.Why, "not approved") {
		t.Fatalf("%+v", s)
	}
}

func TestCodexProjectLayerNeedsTrust(t *testing.T) {
	home, root := env(t)
	user := "[mcp_servers.s]\nurl = \"https://user.example.com/mcp\"\nbearer_token_env_var = \"T\"\n"
	put(t, filepath.Join(root, ".codex", "config.toml"), "[mcp_servers.s]\nurl = \"https://project.example.com/mcp\"\n[mcp_servers.p]\ncommand = \"p\"\n")
	put(t, filepath.Join(home, ".codex", "config.toml"), user)
	if got := Servers("codex", root); got["s"].URL != "https://user.example.com/mcp" || len(got) != 1 {
		t.Fatalf("untrusted project layer read: %+v", got)
	}
	abs, _ := filepath.Abs(root)
	put(t, filepath.Join(home, ".codex", "config.toml"), user+"[projects."+tomlString(abs)+"]\ntrust_level = \"trusted\"\n")
	got := Servers("codex", root)
	// The project replaces url and keeps the user's bearer variable.
	if got["s"].URL != "https://project.example.com/mcp" || got["s"].BearerVar != "T" || got["s"].Scope != "project" || got["p"].Command != "p" {
		t.Fatalf("%+v", got)
	}
}

func TestNotBridgeable(t *testing.T) {
	cases := []struct {
		name, from, def, why string
		lead                 map[string]string
	}{
		{"claude oauth", "claude", `{"type":"http","url":"https://x.example.com","oauth":{"clientId":"c"}}`, "OAuth", nil},
		{"claude no credentials", "claude", `{"type":"http","url":"https://x.example.com"}`, "OAuth", nil},
		{"claude headers helper", "claude", `{"type":"http","url":"https://x.example.com","headersHelper":"/bin/h"}`, "helper", nil},
		{"claude sse", "claude", `{"type":"sse","url":"https://x.example.com","headers":{"A":"b"}}`, "SSE", nil},
		{"claude ws", "claude", `{"type":"ws","url":"wss://x.example.com"}`, "unknown", nil},
		{"claude args read a variable", "claude", `{"command":"x","args":["--token","${T}"]}`, "Codex cannot", map[string]string{"T": secretA}},
		{"claude url with query", "claude", `{"type":"http","url":"https://x.example.com/mcp?key=` + secretA + `","headers":{"A":"b"}}`, "query", nil},
		{"claude unset variable", "claude", `{"type":"http","url":"https://x.example.com","headers":{"A":"${NOT_SET}"}}`, "$NOT_SET", nil},
		{"claude env collides", "claude", `{"command":"x","env":{"LANG":"C"}}`, "already has", nil},
		{"claude env of the agent", "claude", `{"command":"x","env":{"OPENAI_BASE_URL":"https://llm.example.net/v1"}}`, "agent reads too", nil},
		{"claude env of duolog in other case", "claude", `{"command":"x","env":{"duolog_depth":"0"}}`, "duolog keeps", nil},
		{"claude env of duolog", "claude", `{"command":"x","env":{"DUOLOG_DEPTH":"0"}}`, "duolog keeps", nil},
		{"codex auth", "codex", "url = \"https://x.example.com\"\nauth = \"oauth\"\nbearer_token_env_var = \"T\"\n", "OAuth", map[string]string{"T": "t"}},
		{"codex oauth table", "codex", "url = \"https://x.example.com\"\n[mcp_servers.svc.oauth]\nclient_id = \"c\"\n", "OAuth", nil},
		{"codex scopes", "codex", "url = \"https://x.example.com\"\nscopes = [\"read\"]\n", "OAuth", nil},
		{"codex no credentials", "codex", "url = \"https://x.example.com\"\n", "OAuth", nil},
		{"codex headers helper", "codex", "url = \"https://x.example.com\"\nhttp_headers_helper = \"/bin/h\"\n", "helper", nil},
		{"codex remote", "codex", "command = \"x\"\nexperimental_environment = \"remote\"\n", "remote", nil},
		{"codex remote env", "codex", "command = \"x\"\nenv_vars = [{name = \"A\", source = \"remote\"}]\n", "remote", nil},
		{"codex unknown transport", "codex", "enabled = true\n", "unknown", nil},
		{"codex cwd", "codex", "command = \"x\"\ncwd = \"/elsewhere\"\n", "working folder", nil},
		{"codex missing bearer", "codex", "url = \"https://x.example.com\"\nbearer_token_env_var = \"NOT_SET\"\n", "$NOT_SET", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, root := env(t)
			leadEnv(t, c.lead)
			to := "codex"
			if c.from == "codex" {
				to = "claude"
				put(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.svc]\n"+c.def)
			} else {
				claudeState(t, home, root, `{"svc":`+c.def+`}`, "")
			}
			got, failed := Bridge(c.from, to, root, []string{"svc"}, nil, []string{"HOME=" + home, "LANG=en_GB.UTF-8"})
			if got != nil || !strings.Contains(failed["svc"], c.why) {
				t.Fatalf("got %+v, failed %q, want reason with %q", got, failed, c.why)
			}
			noSecret(t, "reason", failed["svc"])
		})
	}
}

func TestBridgeToolAllowlist(t *testing.T) {
	home, root := env(t)
	leadEnv(t, nil)
	claudeState(t, home, root, `{"jira":{"type":"http","url":"https://j.example.com","headers":{"X":"`+secretA+`"}},"wiki":{"command":"w"}}`, "")
	tools := map[string][]string{"jira": {"get_issue", "search"}}
	got, _ := Bridge("claude", "codex", root, []string{"jira", "wiki"}, tools, nil)
	if v, _ := codexValue(got.CodexArgs, "mcp_servers.jira.enabled_tools"); v != `["get_issue","search"]` {
		t.Fatal(got.CodexArgs)
	}
	// A server with no list is bridged whole.
	if _, ok := codexValue(got.CodexArgs, "mcp_servers.wiki.enabled_tools"); ok {
		t.Fatal(got.CodexArgs)
	}

	put(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.jira]\nurl = \"https://j.example.com\"\nbearer_token_env_var = \"T\"\nenabled_tools = [\"get_issue\"]\ndisabled_tools = [\"delete\"]\n[mcp_servers.wiki]\ncommand = \"w\"\nenabled_tools = [\"read\"]\n")
	leadEnv(t, map[string]string{"T": secretA})
	got, _ = Bridge("codex", "claude", root, []string{"jira", "wiki"}, map[string][]string{"wiki": {"read", "list.all"}}, nil)
	// The duolog list wins over the lead's own enabled_tools.
	want := []string{"mcp__jira__get_issue", "mcp__wiki__read", "mcp__wiki__list_all"}
	if !slices.Equal(got.AllowedTools, want) || !slices.Equal(got.DisallowedTools, []string{"mcp__jira__delete"}) {
		t.Fatal(got.AllowedTools, got.DisallowedTools)
	}
}

func TestScanMarksBridgeableWithoutSecrets(t *testing.T) {
	home, root := env(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	onlyPath(t, claude)
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0755); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, ".duolog", ".gitignore"), "*\n")
	claudeState(t, home, root, `{"jira":{"type":"http","url":"https://j.example.com","headers":{"X-Token":"`+secretA+`"}},"notion":{"type":"http","url":"https://n.example.com"}}`, "")
	inv, err := Scan(context.Background(), "claude", root)
	if err != nil {
		t.Fatal(err)
	}
	var jira, notion Item
	for _, it := range inv.Items {
		switch it.Name {
		case "jira":
			jira = it
		case "notion":
			notion = it
		}
	}
	if !jira.Bridgeable || notion.Bridgeable || !strings.Contains(notion.NoBridge, "OAuth") {
		t.Fatal(inv.Items)
	}
	cached, _ := os.ReadFile(filepath.Join(root, ".duolog", "cache", "inventory-claude.json"))
	if len(cached) == 0 {
		t.Fatal("no cache")
	}
	data, _ := json.Marshal(inv)
	for _, text := range []string{string(data), string(cached)} {
		noSecret(t, "inventory", text)
		if strings.Contains(text, "X-Token") {
			t.Fatal(text)
		}
	}
}

func TestResolveBridge(t *testing.T) {
	cfg := defaults()
	cfg.OnMissing.MCP = ActionBridge
	source := inv("claude", Item{Kind: KindMCP, Name: "jira", Bridgeable: true}, Item{Kind: KindMCP, Name: "notion", NoBridge: "it signs in with OAuth"}, Item{Kind: KindMCP, Name: "plugin-only"})
	// A Claude worker's MCP list is never complete, yet a server from the lead's
	// files still counts as missing there.
	unknown := Inventory{Agent: "claude", UnknownKinds: []string{KindMCP}}
	for _, target := range []Inventory{inv("codex"), unknown} {
		source.Agent = "codex"
		if target.Agent == "codex" {
			source.Agent = "claude"
		}
		if r := one(t, target, source, "mcp:jira", cfg); r.Action != ActionBridge || r.Hint != "" {
			t.Fatal(r)
		}
		r := one(t, target, source, "mcp:notion", cfg)
		if r.Action != ActionLeadFirst || !strings.Contains(r.Hint, "cannot be bridged: it signs in with OAuth") || !strings.Contains(r.Hint, "artifact in Read") {
			t.Fatal(r)
		}
	}
	// A server the worker has is never bridged.
	if r := one(t, inv("codex", Item{Kind: KindMCP, Name: "jira"}), source, "mcp:jira", cfg); r.Action != ActionPresent {
		t.Fatal(r)
	}
	// A server from no file keeps the old rule.
	if r := one(t, inv("codex"), source, "mcp:plugin-only", cfg); r.Action != ActionLeadFirst || strings.Contains(r.Hint, "bridged") {
		t.Fatal(r)
	}
	// lead_first stays the default.
	if r := one(t, inv("codex"), source, "mcp:jira", defaults()); r.Action != ActionLeadFirst {
		t.Fatal(r)
	}
}

// The written config and every argument stay free of values; the runner's own
// tests check the file mode and removal.
func TestBridgeOutputHasNoValues(t *testing.T) {
	home, root := env(t)
	leadEnv(t, map[string]string{"T": secretB})
	claudeState(t, home, root, `{"a":{"type":"http","url":"https://a.example.com","headers":{"Authorization":"Bearer `+secretA+`"}}}`, "")
	put(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.b]\nurl = \"https://b.example.com/mcp?t=1\"\nbearer_token_env_var = \"T\"\n[mcp_servers.b.http_headers]\nX = \""+secretC+"\"\n")
	for _, got := range []*runner.MCP{
		func() *runner.MCP { m, _ := Bridge("claude", "codex", root, []string{"a"}, nil, nil); return m }(),
		func() *runner.MCP { m, _ := Bridge("codex", "claude", root, []string{"b"}, nil, nil); return m }(),
	} {
		if got == nil {
			t.Fatal("not bridged")
		}
		noSecret(t, "argv", strings.Join(got.CodexArgs, " "))
		noSecret(t, "config", string(got.ClaudeConfig))
		if !strings.Contains(strings.Join(got.Env, " "), "s3cret") {
			t.Fatal("values missing from the worker env", len(got.Env))
		}
	}
	// A url with a query goes to Claude as a variable too.
	m, _ := Bridge("codex", "claude", root, []string{"b"}, nil, nil)
	if strings.Contains(string(m.ClaudeConfig), "b.example.com") {
		t.Fatal(string(m.ClaudeConfig))
	}
}

func TestLimitOwnServers(t *testing.T) {
	tools := map[string][]string{"jira": {"get_issue"}, "bad name": {"x"}}
	args, allowed := Limit("codex", []string{"jira", "wiki", "bad name"}, tools)
	if strings.Join(args, " ") != `-c mcp_servers.jira.enabled_tools=["get_issue"]` || allowed != nil {
		t.Fatal(args, allowed)
	}
	args, allowed = Limit("claude", []string{"jira", "bad name"}, tools)
	if args != nil || strings.Join(allowed, ",") != "mcp__bad_name__x,mcp__jira__get_issue" {
		t.Fatal(args, allowed)
	}
}

// On Windows variable names ignore case, so a server's Path or duolog_depth
// would replace the worker's own.
func TestBridgeEnvNamesIgnoreCaseOnWindows(t *testing.T) {
	old := windowsEnv
	windowsEnv = true
	t.Cleanup(func() { windowsEnv = old })
	for _, def := range []string{`{"command":"x","env":{"Lang":"C"}}`, `{"command":"x","env":{"Duolog_Depth":"0"}}`} {
		home, root := env(t)
		claudeState(t, home, root, `{"svc":`+def+`}`, "")
		if got, failed := Bridge("claude", "codex", root, []string{"svc"}, nil, []string{"LANG=en_GB.UTF-8", "DUOLOG_DEPTH=1"}); got != nil || failed["svc"] == "" {
			t.Fatal(def, got, failed)
		}
	}
}

// Codex takes a url as text, so a Claude url that reads a variable stays.
func TestBridgeRefusesURLWithVariableForCodex(t *testing.T) {
	home, root := env(t)
	leadEnv(t, map[string]string{"KEY": secretA})
	claudeState(t, home, root, `{"svc":{"type":"http","url":"https://x.example.com/${KEY}/mcp","headers":{"A":"b"}}}`, "")
	got, failed := Bridge("claude", "codex", root, []string{"svc"}, nil, nil)
	if got != nil || !strings.Contains(failed["svc"], "url reads a variable") {
		t.Fatal(got, failed)
	}
}

// A worker whose Codex policy uses the older exclude list gets that list back,
// extended: Codex refuses filters next to it.
func TestBridgeKeepsLegacyShellPolicy(t *testing.T) {
	home, root := env(t)
	leadEnv(t, map[string]string{})
	claudeState(t, home, root, `{"svc":{"type":"http","url":"https://x.example.com/mcp","headers":{"A":"`+secretA+`"}}}`, "")
	put(t, filepath.Join(home, ".codex", "config.toml"), "[shell_environment_policy]\nexclude = [\"AWS_*\"]\n")
	got, failed := Bridge("claude", "codex", root, []string{"svc"}, nil, nil)
	if got == nil {
		t.Fatal(failed)
	}
	if v, ok := codexValue(got.CodexArgs, "shell_environment_policy.exclude"); !ok || v != `["AWS_*","DUOLOG_MCP_*"]` || strings.Contains(strings.Join(got.CodexArgs, " "), "filters") {
		t.Fatal(got.CodexArgs)
	}
}

// -c keys would merge into a Codex table of the same name, a disabled one too.
func TestBridgeRefusesNameTheWorkerHas(t *testing.T) {
	home, root := env(t)
	leadEnv(t, map[string]string{})
	claudeState(t, home, root, `{"svc":{"type":"http","url":"https://x.example.com/mcp","headers":{"A":"b"}}}`, "")
	put(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.svc]\ncommand = \"old\"\nenabled = false\n")
	if got, failed := Bridge("claude", "codex", root, []string{"svc"}, nil, nil); got != nil || !strings.Contains(failed["svc"], "has a server of that name") {
		t.Fatal(got, failed)
	}
}

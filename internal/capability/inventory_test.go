package capability

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func put(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T, name string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "scripts", "bench", "fixtures", "capabilities", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// env gives an empty home and project root. Callers narrow PATH with onlyPath
// after installing fakes, because installing needs the go tool.
func env(t *testing.T) (home, root string) {
	t.Helper()
	home, root = t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	old := Home
	Home = home
	t.Cleanup(func() { Home = old })
	return home, root
}
func onlyPath(t *testing.T, bins ...string) {
	t.Helper()
	dirs := []string{t.TempDir()}
	for _, b := range bins {
		dirs = append(dirs, filepath.Dir(b))
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}
func names(inv Inventory) []string {
	var out []string
	for _, it := range inv.Items {
		out = append(out, it.Kind+":"+it.Name)
	}
	return out
}
func requireItems(t *testing.T, inv Inventory, want ...string) {
	t.Helper()
	if got := strings.Join(names(inv), " "); got != strings.Join(want, " ") {
		t.Fatalf("items = %s, want %s", got, strings.Join(want, " "))
	}
}

func TestScanParsesFixtures(t *testing.T) {
	var claudeList []struct{ ID, InstallPath string }
	if err := json.Unmarshal([]byte(strings.ReplaceAll(fixture(t, "claude-plugin-list.json"), `"id"`, `"ID"`)), &claudeList); err != nil || len(claudeList) != 2 {
		t.Fatal(err)
	}
	t.Run("claude", func(t *testing.T) {
		home, root := env(t)
		onlyPath(t, testbin.Install(t, "claude", testbin.Scenario{}))
		installed := map[string]any{}
		for _, p := range claudeList {
			dir := filepath.Join(home, "cache", strings.SplitN(p.ID, "@", 2)[0])
			installed[p.ID] = []map[string]string{{"installPath": dir}}
			put(t, filepath.Join(dir, "skills", "from-plugin", "SKILL.md"), "x")
		}
		data, _ := json.Marshal(map[string]any{"version": 2, "plugins": installed})
		put(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), string(data))
		put(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"crit@crit":false}}`)
		put(t, filepath.Join(home, ".claude", "skills", "mine", "SKILL.md"), "x")
		put(t, filepath.Join(root, ".claude", "skills", "local", "SKILL.md"), "x")
		put(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"codegraph":{"command":"codegraph"}}}`)
		put(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"user-mcp":{}},"projects":{`+projectsKeys(root)+`}}`)
		inv, err := Scan(context.Background(), "claude", root)
		if err != nil || inv.Unknown {
			t.Fatal(inv, err)
		}
		requireItems(t, inv, "mcp:codegraph", "mcp:proj-mcp", "mcp:user-mcp", "plugin:clangd-lsp", "skill:from-plugin", "skill:local", "skill:mine")
		for _, it := range inv.Items {
			if it.Kind == KindSkill && (!filepath.IsAbs(it.Source) || filepath.Base(it.Source) != "SKILL.md") {
				t.Fatal(it)
			}
		}
	})
	t.Run("codex", func(t *testing.T) {
		home, root := env(t)
		onlyPath(t, testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: fixture(t, "codex-plugin-list.json")}}}))
		put(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.codegraph]\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\n[mcp_servers.off]\nenabled = false\n")
		put(t, filepath.Join(home, ".agents", "skills", "agent-skill", "SKILL.md"), "x")
		put(t, filepath.Join(root, ".codex", "skills", "proj-skill", "SKILL.md"), "x")
		inv, err := Scan(context.Background(), "codex", root)
		if err != nil || inv.Unknown {
			t.Fatal(inv, err)
		}
		got := strings.Join(names(inv), " ")
		for _, want := range []string{"mcp:codegraph", "plugin:sites", "plugin:openai-templates", "skill:agent-skill", "skill:proj-skill"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s missing %s", got, want)
			}
		}
		if strings.Contains(got, "mcp:off") {
			t.Fatal(got)
		}
	})
}

func TestScanAvoidsSlowAndHugeCommands(t *testing.T) {
	home, root := env(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: fixture(t, "codex-plugin-list.json")}}})
	onlyPath(t, claude, codex)
	put(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"a":{}}}`)
	for _, agent := range []string{"claude", "codex"} {
		if _, err := Scan(context.Background(), agent, root); err != nil {
			t.Fatal(err)
		}
	}
	var cargs []string
	cdata, cerr := os.ReadFile(claude + ".args.json")
	if cerr != nil || json.Unmarshal(cdata, &cargs) != nil || strings.Join(cargs, " ") != "plugin list --json" {
		t.Fatalf("claude args %q %v; only the plugin list may run", cdata, cerr)
	}
	var args []string
	data, err := os.ReadFile(codex + ".args.json")
	if err != nil || json.Unmarshal(data, &args) != nil || strings.Join(args, " ") != "plugin list --json" {
		t.Fatalf("codex args %q %v", data, err)
	}
	_ = home
}

func TestScanHasNoSecrets(t *testing.T) {
	home, root := env(t)
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: fixture(t, "codex-plugin-list.json")}}})
	onlyPath(t, claude, codex)
	put(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"svc":{"command":"x","env":{"API_TOKEN":"s3cret-value"},"headers":{"Authorization":"Bearer s3cret-value"}}}}`)
	put(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.svc]\ncommand = \"x\"\n[mcp_servers.svc.env]\nAPI_TOKEN = \"s3cret-value\"\n")
	var all strings.Builder
	for _, agent := range []string{"claude", "codex"} {
		inv, err := Scan(context.Background(), agent, root)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(inv)
		all.Write(data)
		requireMCP := false
		for _, it := range inv.Items {
			requireMCP = requireMCP || (it.Kind == KindMCP && it.Name == "svc")
		}
		if !requireMCP {
			t.Fatalf("%s: svc missing", agent)
		}
	}
	filepath.Walk(filepath.Join(root, ".duolog"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			data, _ := os.ReadFile(p)
			all.Write(data)
		}
		return nil
	})
	for _, bad := range []string{"s3cret-value", "API_TOKEN", "Authorization", `"env"`} {
		if strings.Contains(all.String(), bad) {
			t.Fatalf("%q leaked", bad)
		}
	}
}

func TestScanMissingAgentIsEmpty(t *testing.T) {
	_, root := env(t)
	onlyPath(t)
	inv, err := Scan(context.Background(), "codex", root)
	if err != nil || len(inv.Items) != 0 || !inv.Unknown || inv.Agent != "codex" {
		t.Fatal(inv, err)
	}
}

func TestCacheInvalidatedByConfigChange(t *testing.T) {
	home, root := env(t)
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o755); err != nil {
		t.Fatal(err)
	}
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: fixture(t, "codex-plugin-list.json")}}})
	onlyPath(t, codex)
	config := filepath.Join(home, ".codex", "config.toml")
	put(t, config, "model = \"a\"\n")
	scan := func() Inventory {
		t.Helper()
		inv, err := Scan(context.Background(), "codex", root)
		if err != nil || inv.Unknown {
			t.Fatal(inv, err)
		}
		return inv
	}
	ran := func() bool {
		_, err := os.Stat(codex + ".args.json")
		os.Remove(codex + ".args.json")
		return err == nil
	}
	first := scan()
	if !ran() {
		t.Fatal("first scan did not run the command")
	}
	second := scan()
	if ran() || strings.Join(names(first), " ") != strings.Join(names(second), " ") {
		t.Fatal("unchanged config ran the command again or changed the result")
	}
	put(t, config, "model = \"b\"\n")
	scan()
	if !ran() {
		t.Fatal("changed config did not scan again")
	}
}

func TestDiffIsDirectional(t *testing.T) {
	a := Inventory{Agent: "claude", Items: []Item{{Kind: "skill", Name: "x"}, {Kind: "mcp", Name: "y"}}}
	b := Inventory{Agent: "codex", Items: []Item{{Kind: "mcp", Name: "y"}, {Kind: "plugin", Name: "z"}}}
	if got := Diff(a, b); len(got) != 1 || got[0].Name != "x" {
		t.Fatal(got)
	}
	if got := Diff(b, a); len(got) != 1 || got[0].Name != "z" {
		t.Fatal(got)
	}
}

func TestHasDuolog(t *testing.T) {
	home, root := env(t)
	if HasDuolog("codex", root) || HasDuolog("claude", root) {
		t.Fatal("empty home has duolog")
	}
	put(t, filepath.Join(root, ".codex", "hooks.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"command":"duolog gate"}]}]}}`)
	put(t, filepath.Join(home, ".claude", "settings.json"), `{"hooks":{"PostToolUse":[{"hooks":[{"command":"duolog hook PostToolUse --agent claude"}]}]}}`)
	if !HasDuolog("codex", root) || !HasDuolog("claude", root) {
		t.Fatal("hooks not found")
	}
}

// A project folder named duolog-fork in a trusted path is not an install.
func TestHasDuologIgnoresTheWordInAPath(t *testing.T) {
	home, root := env(t)
	put(t, filepath.Join(home, ".codex", "config.toml"), "[projects.\"/work/duolog-fork\"]\ntrust_level = \"trusted\"\n")
	put(t, filepath.Join(home, ".claude", "settings.json"), `{"env":{"X":"duolog"}}`)
	if HasDuolog("codex", root) || HasDuolog("claude", root) {
		t.Fatal("a mention of the word counted as an install")
	}
	put(t, filepath.Join(home, ".codex", "config.toml"), "[plugins.\"duolog@duolog\"]\nenabled = true\n")
	if !HasDuolog("codex", root) {
		t.Fatal("the enabled Codex plugin was not found")
	}
}

// projectsKeys writes the project under both the OS path and its slash form,
// each JSON-escaped, so the lookup works on every OS.
func projectsKeys(root string) string {
	var parts []string
	for _, k := range []string{root, filepath.ToSlash(root)} {
		b, _ := json.Marshal(k)
		parts = append(parts, string(b)+`:{"mcpServers":{"proj-mcp":{}}}`)
	}
	if parts[0] == parts[1] {
		parts = parts[:1]
	}
	return strings.Join(parts, ",")
}

func TestScanMarksIncompleteKindsUnknown(t *testing.T) {
	_, root := env(t)
	onlyPath(t, testbin.Install(t, "claude", testbin.Scenario{}))
	inv, err := Scan(context.Background(), "claude", root)
	if err != nil || inv.Unknown || !inv.KindUnknown(KindMCP) || inv.KindUnknown(KindSkill) {
		t.Fatal(inv, err)
	}
}

func TestScanCodexWithPluginsLeavesSkillAndMCPUnknown(t *testing.T) {
	_, root := env(t)
	onlyPath(t, testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: fixture(t, "codex-plugin-list.json")}}}))
	inv, err := Scan(context.Background(), "codex", root)
	if err != nil || inv.Unknown || !inv.KindUnknown(KindSkill) || !inv.KindUnknown(KindMCP) || inv.KindUnknown(KindPlugin) {
		t.Fatal(inv, err)
	}
}

func TestDiffSkipsUnknownKinds(t *testing.T) {
	a := Inventory{Agent: "codex", Items: []Item{{Kind: KindMCP, Name: "x"}, {Kind: KindSkill, Name: "s"}}}
	b := Inventory{Agent: "claude", UnknownKinds: []string{KindMCP}}
	d := Diff(a, b)
	if len(d) != 1 || d[0].Name != "s" {
		t.Fatal(d)
	}
}

func TestClaudePluginsFromRealList(t *testing.T) {
	home, root := env(t)
	onlyPath(t, testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: fixture(t, "claude-plugin-list.json")}}}))
	inv, err := Scan(context.Background(), "claude", root)
	if err != nil || inv.Unknown || inv.KindUnknown(KindPlugin) || inv.KindUnknown(KindSkill) {
		t.Fatal(inv, err)
	}
	got := strings.Join(names(inv), " ")
	if !strings.Contains(got, "plugin:clangd-lsp") || !strings.Contains(got, "plugin:crit") {
		t.Fatal(got)
	}
	_ = home
}

func TestCodexPluginFailureKeepsOtherKindsKnown(t *testing.T) {
	home, root := env(t)
	onlyPath(t, testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: "not json"}}}))
	put(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.codegraph]\ncommand = \"x\"\n")
	inv, err := Scan(context.Background(), "codex", root)
	if err != nil || inv.Unknown || !inv.KindUnknown(KindPlugin) || inv.KindUnknown(KindMCP) {
		t.Fatal(inv, err)
	}
	requireItems(t, inv, "mcp:codegraph")
}

func TestClaudeCapabilityConfigOverride(t *testing.T) {
	home, root := env(t)
	custom := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	put(t, filepath.Join(home, ".claude", "skills", "ignored", "SKILL.md"), "ignored")
	put(t, filepath.Join(custom, "skills", "visible", "SKILL.md"), "visible")
	put(t, filepath.Join(custom, ".claude.json"), `{"mcpServers":{"override-server":{}}}`)
	fake := testbin.Install(t, "claude", testbin.Scenario{Stdout: "[]"})
	onlyPath(t, fake)
	inv, err := Scan(context.Background(), "claude", root)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names(inv), "\n")
	if strings.Contains(got, "ignored") || !strings.Contains(got, "visible") || !strings.Contains(got, "override-server") {
		t.Fatal(got)
	}
}

package wiring

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// The capability tests that need both agents run the two adapters' Caps,
// each with the test's home folder in its Home field.

func capsPut(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
}

// capsOf gives each agent's Caps with an empty home and a project root.
// Callers narrow PATH with onlyPath after installing fakes, because installing
// needs the go tool.
func capsOf(t *testing.T) (all map[string]Caps, home, root string) {
	t.Helper()
	home, root = t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return map[string]Caps{"claude": claude.Caps{Home: home}, "codex": codex.Caps{Home: home}}, home, root
}

func capsNames(inv caps.Inventory) []string {
	var out []string
	for _, it := range inv.Items {
		out = append(out, it.Kind+":"+it.Name)
	}
	return out
}

func codexPlugins(t *testing.T) string {
	t.Helper()
	return testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: fixture(t, filepath.Join("capabilities", "codex-plugin-list.json"))}}})
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

func TestScanParsesFixtures(t *testing.T) {
	var claudeList []struct{ ID, InstallPath string }
	if err := json.Unmarshal([]byte(strings.ReplaceAll(fixture(t, filepath.Join("capabilities", "claude-plugin-list.json")), `"id"`, `"ID"`)), &claudeList); err != nil || len(claudeList) != 2 {
		t.Fatal(err)
	}
	t.Run("claude", func(t *testing.T) {
		all, home, root := capsOf(t)
		onlyPath(t, testbin.Install(t, "claude", testbin.Scenario{}))
		installed := map[string]any{}
		for _, p := range claudeList {
			dir := filepath.Join(home, "cache", strings.SplitN(p.ID, "@", 2)[0])
			installed[p.ID] = []map[string]string{{"installPath": dir}}
			capsPut(t, filepath.Join(dir, "skills", "from-plugin", "SKILL.md"), "x")
		}
		data, _ := json.Marshal(map[string]any{"version": 2, "plugins": installed})
		capsPut(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), string(data))
		capsPut(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"crit@crit":false}}`)
		capsPut(t, filepath.Join(home, ".claude", "skills", "mine", "SKILL.md"), "x")
		capsPut(t, filepath.Join(root, ".claude", "skills", "local", "SKILL.md"), "x")
		capsPut(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"codegraph":{"command":"codegraph"}}}`)
		capsPut(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"user-mcp":{}},"projects":{`+projectsKeys(root)+`}}`)
		inv, err := all["claude"].Scan(context.Background(), root, os.Environ())
		if err != nil || inv.Unknown {
			t.Fatal(inv, err)
		}
		want := "mcp:codegraph mcp:proj-mcp mcp:user-mcp plugin:clangd-lsp skill:from-plugin skill:local skill:mine"
		if got := strings.Join(capsNames(inv), " "); got != want {
			t.Fatalf("items = %s, want %s", got, want)
		}
		for _, it := range inv.Items {
			if it.Kind == caps.KindSkill && (!filepath.IsAbs(it.Source) || filepath.Base(it.Source) != "SKILL.md") {
				t.Fatal(it)
			}
		}
	})
	t.Run("codex", func(t *testing.T) {
		all, home, root := capsOf(t)
		onlyPath(t, codexPlugins(t))
		capsPut(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.codegraph]\ncommand = \"codegraph\"\nargs = [\"serve\", \"--mcp\"]\n[mcp_servers.off]\nenabled = false\n")
		capsPut(t, filepath.Join(home, ".agents", "skills", "agent-skill", "SKILL.md"), "x")
		capsPut(t, filepath.Join(root, ".codex", "skills", "proj-skill", "SKILL.md"), "x")
		inv, err := all["codex"].Scan(context.Background(), root, os.Environ())
		if err != nil || inv.Unknown {
			t.Fatal(inv, err)
		}
		got := strings.Join(capsNames(inv), " ")
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
	all, _, root := capsOf(t)
	claudeBin := testbin.Install(t, "claude", testbin.Scenario{})
	codexBin := codexPlugins(t)
	onlyPath(t, claudeBin, codexBin)
	capsPut(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"a":{}}}`)
	for _, agent := range []string{"claude", "codex"} {
		if _, err := all[agent].Scan(context.Background(), root, os.Environ()); err != nil {
			t.Fatal(err)
		}
	}
	for _, bin := range []string{claudeBin, codexBin} {
		var args []string
		data, err := os.ReadFile(bin + ".args.json")
		if err != nil || json.Unmarshal(data, &args) != nil || strings.Join(args, " ") != "plugin list --json" {
			t.Fatalf("%s args %q %v; only the plugin list may run", filepath.Base(bin), data, err)
		}
	}
}

func TestScanHasNoSecrets(t *testing.T) {
	all, home, root := capsOf(t)
	onlyPath(t, testbin.Install(t, "claude", testbin.Scenario{}), codexPlugins(t))
	capsPut(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"svc":{"command":"x","env":{"API_TOKEN":"s3cret-value"},"headers":{"Authorization":"Bearer s3cret-value"}}}}`)
	capsPut(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.svc]\ncommand = \"x\"\n[mcp_servers.svc.env]\nAPI_TOKEN = \"s3cret-value\"\n")
	var text strings.Builder
	for _, agent := range []string{"claude", "codex"} {
		inv, err := all[agent].Scan(context.Background(), root, os.Environ())
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(inv)
		text.Write(data)
		if !slices.Contains(capsNames(inv), "mcp:svc") {
			t.Fatalf("%s: svc missing", agent)
		}
	}
	_ = filepath.Walk(filepath.Join(root, ".duolog"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			data, _ := os.ReadFile(p)
			text.Write(data)
		}
		return nil
	})
	for _, bad := range []string{"s3cret-value", "API_TOKEN", "Authorization", `"env"`} {
		if strings.Contains(text.String(), bad) {
			t.Fatalf("%q leaked", bad)
		}
	}
}

// Re-review, Minor 2: the worker's inventory reads the settings folders its
// own environment selects. A server defined only in the caller's
// CLAUDE_CONFIG_DIR or CODEX_HOME, which env_passthrough does not pass, is
// missing for the worker, so Resolve bridges it or asks the lead.
func TestScanEnvReadsTheWorkersSettings(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			_, _, root := capsOf(t)
			c, _ := Find(agent)
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			caller := t.TempDir()
			scenario := testbin.Scenario{Stdout: "[]"}
			if agent == "claude" {
				capsPut(t, filepath.Join(caller, ".claude.json"), `{"mcpServers":{"tracker":{"command":"tracker"}}}`)
				t.Setenv("CLAUDE_CONFIG_DIR", caller)
			} else {
				scenario = testbin.Scenario{Responses: map[string]testbin.Scenario{"plugin list --json": {Stdout: `{"installed":[]}`}}}
				capsPut(t, filepath.Join(caller, "config.toml"), "[mcp_servers.tracker]\ncommand = \"tracker\"\n")
				t.Setenv("CODEX_HOME", caller)
			}
			onlyPath(t, testbin.Install(t, agent, scenario))
			own, err := c.Caps.Scan(context.Background(), root, os.Environ())
			if err != nil || !slices.Contains(capsNames(own), "mcp:tracker") {
				t.Fatalf("duolog's own scan %v, %v; want mcp:tracker", capsNames(own), err)
			}
			worker := []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH")}
			inv, err := c.Caps.Scan(context.Background(), root, worker)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(capsNames(inv), "mcp:tracker") {
				t.Errorf("the worker's scan lists mcp:tracker from the caller-only folder: %v", capsNames(inv))
			}
			if _, ok := c.Caps.Servers(root, worker)["tracker"]; ok {
				t.Error("Servers lists the caller-only tracker for the worker")
			}
			passed := append(slices.Clone(worker), "CLAUDE_CONFIG_DIR="+caller, "CODEX_HOME="+caller)
			if inv, _ := c.Caps.Scan(context.Background(), root, passed); !slices.Contains(capsNames(inv), "mcp:tracker") {
				t.Errorf("with the folder passed on, the worker's scan lacks mcp:tracker: %v", capsNames(inv))
			}
		})
	}
}

func TestHasDuolog(t *testing.T) {
	all, home, root := capsOf(t)
	if all["codex"].HasDuolog(root, os.Environ()) || all["claude"].HasDuolog(root, os.Environ()) {
		t.Fatal("empty home has duolog")
	}
	capsPut(t, filepath.Join(root, ".codex", "hooks.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"command":"duolog gate"}]}]}}`)
	capsPut(t, filepath.Join(home, ".claude", "settings.json"), `{"hooks":{"PostToolUse":[{"hooks":[{"command":"duolog hook PostToolUse --agent claude"}]}]}}`)
	if !all["codex"].HasDuolog(root, os.Environ()) || !all["claude"].HasDuolog(root, os.Environ()) {
		t.Fatal("hooks not found")
	}
}

// A project folder named duolog-fork in a trusted path is not an install.
func TestHasDuologIgnoresTheWordInAPath(t *testing.T) {
	all, home, root := capsOf(t)
	capsPut(t, filepath.Join(home, ".codex", "config.toml"), "[projects.\"/work/duolog-fork\"]\ntrust_level = \"trusted\"\n")
	capsPut(t, filepath.Join(home, ".claude", "settings.json"), `{"env":{"X":"duolog"}}`)
	if all["codex"].HasDuolog(root, os.Environ()) || all["claude"].HasDuolog(root, os.Environ()) {
		t.Fatal("a mention of the word counted as an install")
	}
	capsPut(t, filepath.Join(home, ".codex", "config.toml"), "[plugins.\"duolog@duolog\"]\nenabled = true\n")
	if !all["codex"].HasDuolog(root, os.Environ()) {
		t.Fatal("the enabled Codex plugin was not found")
	}
}

// mcp_tools limits the servers a worker has itself: Codex gets enabled_tools
// for a server its config defines and a bare key can hold, Claude gets
// --allowedTools with the names folded.
func TestLimitOwnServers(t *testing.T) {
	all, home, root := capsOf(t)
	capsPut(t, filepath.Join(home, ".codex", "config.toml"), "[mcp_servers.jira]\ncommand = \"j\"\n[mcp_servers.wiki]\ncommand = \"w\"\n")
	tools := map[string][]string{"jira": {"get_issue"}, "bad name": {"x"}}
	args, allowed := all["codex"].Limit(root, []string{"jira", "wiki", "bad name"}, tools, os.Environ())
	if strings.Join(args, " ") != `-c mcp_servers.jira.enabled_tools=["get_issue"]` || allowed != nil {
		t.Fatal(args, allowed)
	}
	args, allowed = all["claude"].Limit(root, []string{"jira", "bad name"}, tools, os.Environ())
	if args != nil || strings.Join(allowed, ",") != "mcp__bad_name__x,mcp__jira__get_issue" {
		t.Fatal(args, allowed)
	}
}

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/pelletier/go-toml/v2"
)

func TestInstallCommandProject(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Chdir(root)
	cmd := newRootCommand(testDeps(t))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"install", "--scope", "project", "--agent", "codex"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/hooks") {
		t.Fatalf("output: %q", out.String())
	}
	// The skills, the MCP server table in Codex's config.toml and its stamp
	// are what a Codex install writes at user scope.
	var names []string
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != ".agents .codex .duolog" {
		t.Fatalf("home changed: %v", names)
	}
	config := filepath.Join(home, ".codex", "config.toml")
	if data, err := os.ReadFile(config); err != nil || !strings.Contains(string(data), "[mcp_servers.duolog]\n") {
		t.Fatalf("%s: %s, %v", config, data, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "duolog", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	cmd = newRootCommand(testDeps(t))
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"uninstall", "--scope", "project", "--agent", "codex"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "duolog")); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the user skill: %v", err)
	}
	if !strings.Contains(out.String(), "duolog skills removed for all projects") || !strings.Contains(out.String(), "duolog MCP server removed for all projects") {
		t.Fatalf("output: %q", out.String())
	}
	if data, err := os.ReadFile(config); err != nil || strings.Contains(string(data), "mcp_servers") {
		t.Fatalf("%s after uninstall: %s, %v", config, data, err)
	}
}

// codexHead is the start of duolog's MCP server table in Codex's
// config.toml; its env_vars line follows.
const codexHead = "[mcp_servers.duolog]\ncommand = \"duolog\"\nargs = [\"mcp\"]\ntool_timeout_sec = 660\nenv_vars = ["

// withCodexTable reports whether after is text with duolog's table added
// at its end, after a blank line, and returns the env_vars names.
func withCodexTable(t *testing.T, after, text string) ([]string, bool) {
	t.Helper()
	if text != "" {
		text += "\n"
	}
	rest, ok := strings.CutPrefix(after, text+codexHead)
	if !ok || !strings.HasSuffix(rest, "]\n") || strings.Count(rest, "\n") != 1 {
		return nil, false
	}
	var doc struct {
		Servers map[string]struct {
			EnvVars []string `toml:"env_vars"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal([]byte(after), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Servers["duolog"].EnvVars, true
}

// The table forwards, besides duolog's own names, the env_passthrough and
// paid_runs env names of the config at install time, so that a delegate run
// the lead's server starts gets them; a later install adds new ones and
// keeps the old.
func TestInstallCodexMCPForwardsConfiguredEnv(t *testing.T) {
	isolatedRouteConfig(t)
	home, root := os.Getenv("HOME"), t.TempDir()
	chdir(t, root)
	putWorktreeFile(t, filepath.Join(root, ".duolog", "config.yaml"), "version: 1\nenv_passthrough: [PATH, HOME, MY_TOOL_TOKEN]\n")
	install := func() []string {
		t.Helper()
		cmd := newRootCommand(testDeps(t))
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetArgs([]string{"install", "--scope", "project", "--agent", "codex"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		names, ok := withCodexTable(t, string(data), "")
		if !ok {
			t.Fatalf("config.toml:\n%s", data)
		}
		return names
	}
	names := install()
	for _, want := range []string{"MY_TOOL_TOKEN", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "DUOLOG_DEPTH", "DUOLOG_PEER", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "REX_SESSION"} {
		if !slices.Contains(names, want) {
			t.Errorf("env_vars %q lacks %s", names, want)
		}
	}
	putWorktreeFile(t, filepath.Join(root, ".duolog", "config.yaml"), "version: 1\nenv_passthrough: [PATH, HOME, OTHER_TOKEN]\n")
	if names = install(); !slices.Contains(names, "MY_TOOL_TOKEN") || !slices.Contains(names, "OTHER_TOKEN") {
		t.Fatalf("env_vars after a second install %q", names)
	}
}

// TestInstallEndsWithCodexSetup kills omitted notes and notes for disabled or user installs.
func TestInstallEndsWithCodexSetup(t *testing.T) {
	for _, tc := range []struct {
		name, scope, agent            string
		disabled, folder, hooks, want bool
	}{
		{name: "missing both", scope: "project", agent: "codex", want: true},
		{name: "missing folder", scope: "project", agent: "both", hooks: true, want: true},
		{name: "missing hooks", scope: "project", agent: "codex", folder: true, want: true},
		{name: "changed hooks", scope: "project", agent: "codex", folder: true, hooks: true, want: true},
		{name: "ready", scope: "project", agent: "codex", folder: true, hooks: true},
		{name: "disabled", scope: "project", agent: "codex", disabled: true},
		{name: "user", scope: "user", agent: "codex"},
		{name: "claude with codex enabled", scope: "project", agent: "claude", want: true},
		{name: "claude with codex disabled", scope: "project", agent: "claude", disabled: true},
		{name: "invalid unrelated config", scope: "project", agent: "codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, home := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, "alternate"))
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
			t.Chdir(root)
			if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0700); err != nil {
				t.Fatal(err)
			}
			if tc.disabled {
				if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("agents:\n  codex:\n    enabled: false\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Kill a diagnostic that turns successful install into config validation.
			if tc.name == "invalid unrelated config" {
				if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("policy:\n  ask_timeout_seconds: invalid\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "changed hooks" {
				hooks, err := launchtest.HooksJSON("codex", 30)
				if err != nil {
					t.Fatal(err)
				}
				hooks = []byte(strings.ReplaceAll(string(hooks), "--event PreToolUse", "--event PreToolUse --other"))
				if err := os.MkdirAll(filepath.Join(root, ".codex"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, ".codex", "hooks.json"), hooks, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "ready" {
				// Mutation killed: treating an invented approval as a current installed gate.
				hooks, err := launchtest.HooksJSON("codex", 630)
				if err != nil {
					t.Fatal(err)
				}
				putWorktreeFile(t, filepath.Join(root, ".codex", "hooks.json"), string(hooks))
			}
			text := ""
			if tc.folder {
				text += fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", root)
			}
			if tc.hooks {
				text += fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = %q\n", filepath.Join(root, ".codex", "hooks.json")+":PreToolUse:0:0", fixtureCodexApproval(t, 630))
			}
			if err := os.MkdirAll(os.Getenv("CODEX_HOME"), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(os.Getenv("CODEX_HOME"), "config.toml")
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := newRootCommand(testDeps(t))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"install", "--scope", tc.scope, "--agent", tc.agent})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			last := lines[len(lines)-1]
			found := strings.Contains(out.String(), "accept the trust question")
			if found != tc.want {
				t.Fatalf("note found=%t: %s", found, out.String())
			}
			if tc.want && (!strings.Contains(last, root) || !strings.Contains(last, "open codex") || !strings.Contains(last, "approve the hooks in /hooks")) {
				t.Fatalf("last line: %s", last)
			}
			// Install adds only duolog's MCP server table, after a blank line.
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, added := withCodexTable(t, string(after), text); tc.agent == "claude" && string(after) != text || tc.agent != "claude" && !added {
				t.Fatalf("trust changed: %s", after)
			}
		})
	}
}

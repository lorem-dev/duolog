package main

import (
	"bytes"
	"fmt"
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// The skills are the one thing a Codex install writes at user scope.
	if entries, _ := os.ReadDir(home); len(entries) != 1 || entries[0].Name() != ".agents" {
		t.Fatalf("home changed: %v", entries)
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
	if !strings.Contains(out.String(), "duolog skills removed for all projects") {
		t.Fatalf("output: %q", out.String())
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
			text := ""
			if tc.folder {
				text += fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", root)
			}
			if tc.hooks {
				text += fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", filepath.Join(root, ".codex", "hooks.json")+":PreToolUse:0:0")
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
			after, err := os.ReadFile(path)
			if err != nil || string(after) != text {
				t.Fatalf("trust changed: %s, %v", after, err)
			}
		})
	}
}

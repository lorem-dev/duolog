package trust

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/install"
)

// Codex review 6: the preflight refuses a gate it cannot establish now. A
// Codex approval of a hook whose command changed since, or a Claude plugin
// that is enabled but not installed, is not a gate.
func TestGateProblemNeedsTheCurrentGate(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		root := t.TempDir()
		fixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"approved-hash\"\n", filepath.Join(root, ".codex", "hooks.json")+":pre_tool_use:0:0"))
		generated, err := install.HooksJSON("codex", 30)
		if err != nil {
			t.Fatal(err)
		}
		writeHook(t, root, string(generated))
		if s, err := Hooks("codex", root); err != nil || GateProblem("codex", s, true) != "" {
			t.Fatalf("duolog's own hook: %+v %v %q", s, err, GateProblem("codex", s, true))
		}
		// Still recognisable as the gate, but not the command that was approved.
		changed := strings.Replace(string(generated), "--event PreToolUse", "--event PreToolUse --agent claude", 2)
		if changed == string(generated) {
			t.Fatal("fixture did not change")
		}
		writeHook(t, root, changed)
		s, err := Hooks("codex", root)
		if problem := GateProblem("codex", s, true); err != nil || !s.Trusted || !strings.Contains(problem, "not duolog's gate command") {
			t.Fatalf("%+v %v %q", s, err, problem)
		}
	})
	t.Run("claude", func(t *testing.T) {
		root := t.TempDir()
		fixture(t, "")
		home := os.Getenv("HOME")
		settings := filepath.Join(home, ".claude", "settings.json")
		writeFile(t, settings, `{"enabledPlugins":{"duolog@duolog":true}}`)
		s, err := Hooks("claude", root)
		if problem := GateProblem("claude", s, true); err != nil || !strings.Contains(problem, "enabled but not installed") {
			t.Fatalf("%+v %v %q", s, err, problem)
		}
		plugin := filepath.Join(home, ".claude", "plugins", "cache", "duolog")
		hooks, err := os.ReadFile(filepath.Join("..", "..", "plugin", "claude", "hooks", "hooks.json"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(plugin, "hooks", "hooks.json"), string(hooks))
		writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), fmt.Sprintf(`{"plugins":{"duolog@duolog":[{"installPath":%q}]}}`, plugin))
		if s, err = Hooks("claude", root); err != nil || GateProblem("claude", s, true) != "" {
			t.Fatalf("installed plugin: %+v %v %q", s, err, GateProblem("claude", s, true))
		}
		// Gate hooks in the settings need no plugin.
		writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{"plugins":{}}`)
		writeFile(t, settings, `{"enabledPlugins":{"duolog@duolog":true},"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"duolog gate --agent claude --event PreToolUse"}]}]}}`)
		if s, err = Hooks("claude", root); err != nil || GateProblem("claude", s, true) != "" {
			t.Fatalf("settings gate: %+v %v %q", s, err, GateProblem("claude", s, true))
		}
	})
}

// Re-review m6: a plugin install scoped to another project runs no hook here.
func TestPluginInstallForAnotherProject(t *testing.T) {
	root := t.TempDir()
	fixture(t, "")
	home := os.Getenv("HOME")
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":true}}`)
	plugin := filepath.Join(home, ".claude", "plugins", "cache", "duolog")
	hooks, err := os.ReadFile(filepath.Join("..", "..", "plugin", "claude", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(plugin, "hooks", "hooks.json"), string(hooks))
	installed := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	for _, tc := range []struct {
		entry string
		ok    bool
	}{
		{fmt.Sprintf(`{"scope":"project","projectPath":%q,"installPath":%q}`, t.TempDir(), plugin), false},
		{fmt.Sprintf(`{"scope":"local","projectPath":%q,"installPath":%q}`, t.TempDir(), plugin), false},
		{fmt.Sprintf(`{"scope":"project","projectPath":%q,"installPath":%q}`, root, plugin), true},
		{fmt.Sprintf(`{"scope":"user","installPath":%q}`, plugin), true},
		{fmt.Sprintf(`{"installPath":%q}`, plugin), true},
	} {
		writeFile(t, installed, `{"plugins":{"duolog@duolog":[`+tc.entry+`]}}`)
		s, err := Hooks("claude", root)
		if problem := GateProblem("claude", s, true); err != nil || (problem == "") != tc.ok {
			t.Errorf("%s: %+v %v %q", tc.entry, s, err, problem)
		}
	}
}

// Re-review Important 3: an approval given for an older duolog's gate command
// does not count after duolog install rewrites the command, until Codex
// records a new hash for it.
func TestStaleCodexApprovalAfterReinstall(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, ".codex", "hooks.json") + ":pre_tool_use:0:0"
	fixture(t, fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"old-hash\"\n", key))
	home := os.Getenv("HOME")
	generated, err := install.HooksJSON("codex", 150)
	if err != nil {
		t.Fatal(err)
	}
	reinstall := func() {
		t.Helper()
		if _, err := install.Run(install.Options{Scope: "project", Agent: "codex", Home: home, Root: root}); err != nil {
			t.Fatal(err)
		}
	}
	// An unchanged gate keeps its approval through a reinstall.
	writeHook(t, root, string(generated))
	reinstall()
	if s, err := Hooks("codex", root); err != nil || GateProblem("codex", s, true) != "" {
		t.Fatalf("unchanged gate: %+v %v %q", s, err, GateProblem("codex", s, true))
	}
	// An older duolog's command, approved, then rewritten by install.
	older := strings.ReplaceAll(string(generated), "exec duolog gate", "exec duolog --old gate")
	if older == string(generated) {
		t.Fatal("fixture did not change")
	}
	writeHook(t, root, older)
	reinstall()
	if data, _ := os.ReadFile(filepath.Join(root, ".codex", "hooks.json")); strings.Contains(string(data), "--old") {
		t.Fatal("install kept the older command")
	}
	s, err := Hooks("codex", root)
	if problem := GateProblem("codex", s, true); err != nil || !strings.Contains(problem, "approve it again in /hooks") {
		t.Fatalf("stale approval: %+v %v %q", s, err, problem)
	}
	// Approving it again in /hooks gives a new hash.
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"new-hash\"\n", key))
	if s, err := Hooks("codex", root); err != nil || GateProblem("codex", s, true) != "" {
		t.Fatalf("approved again: %+v %v %q", s, err, GateProblem("codex", s, true))
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGateProblem(t *testing.T) {
	for _, tc := range []struct {
		agent  string
		s      State
		folder bool
		want   string
	}{
		{"claude", State{Trusted: true, Installed: true}, true, ""},
		{"claude", State{Trusted: true, Installed: true, HooksOff: "/x/settings.json"}, true, "/x/settings.json sets disableAllHooks"},
		{"claude", State{Trusted: true}, true, "Claude has no duolog gate here"},
		{"codex", State{Trusted: true}, true, ""},
		{"codex", State{}, true, "Codex hooks are not approved; open /hooks"},
		{"codex", State{Trusted: true}, false, "Codex does not trust this folder"},
	} {
		if got := GateProblem(tc.agent, tc.s, tc.folder); got != tc.want {
			t.Errorf("%s %+v %t: %q want %q", tc.agent, tc.s, tc.folder, got, tc.want)
		}
	}
}

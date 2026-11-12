package wiring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The plugin's copy of a duolog hook steps aside only when Claude started it
// from duolog's plugin and a settings file of the project or the user runs
// the same hook for the same event; a hook from a settings file never does.
func TestPluginHookDuplicate(t *testing.T) {
	home, project, plugin, other := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	pluginHooks, err := claude.PluginHooksJSON(630)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(plugin, "hooks", "hooks.json"), pluginHooks)
	writeFile(t, filepath.Join(other, "hooks", "hooks.json"), []byte(`{"hooks":{}}`))
	native, err := claude.HooksJSON(630)
	if err != nil {
		t.Fatal(err)
	}
	env := func(pluginRoot string) []string {
		return []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"),
			"CLAUDE_PROJECT_DIR=" + project, "CLAUDE_PLUGIN_ROOT=" + pluginRoot}
	}
	settings := filepath.Join(project, ".claude", "settings.json")

	// No native hooks yet: the plugin's hooks do the work.
	if PluginHookDuplicate(env(plugin), "PreToolUse", "gate") {
		t.Fatal("stepped aside with no native hooks")
	}
	writeFile(t, settings, native)
	for _, c := range []struct {
		event, verb string
	}{
		{"PreToolUse", "gate"}, {"PermissionRequest", "gate"}, {"SessionStart", "hook SessionStart"},
		{"SessionStart", "hook wake"}, {"PostToolUse", "hook PostToolUse"}, {"Stop", "hook Stop"},
	} {
		if !PluginHookDuplicate(env(plugin), c.event, c.verb) {
			t.Errorf("%s %s: did not step aside beside native hooks", c.event, c.verb)
		}
	}
	// A hook from a settings file has no plugin root; another plugin is not
	// duolog's; a hook the settings do not run is not a duplicate.
	if PluginHookDuplicate(env(""), "PreToolUse", "gate") {
		t.Error("a settings hook stepped aside")
	}
	if PluginHookDuplicate(env(other), "PreToolUse", "gate") {
		t.Error("another plugin stepped aside")
	}
	if PluginHookDuplicate(env(plugin), "Notification", "hook Notification") {
		t.Error("stepped aside for an event the settings do not run")
	}

	// A settings gate behind a narrower matcher does not run for every tool,
	// so the plugin's gate stays.
	writeFile(t, settings, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"duolog","args":["gate","--agent","claude","--event","PreToolUse"]}]}]}}`))
	if PluginHookDuplicate(env(plugin), "PreToolUse", "gate") {
		t.Error("stepped aside for a gate that covers only Read")
	}

	// User settings count as well.
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), native)
	if !PluginHookDuplicate(env(plugin), "UserPromptSubmit", "hook UserPromptSubmit") {
		t.Error("did not step aside beside user hooks")
	}
}

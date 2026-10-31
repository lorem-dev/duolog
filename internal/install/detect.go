package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/trusthome"
)

// HookGroup is one entry of a settings or hooks file, as both agents write it.
type HookGroup struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type           string   `json:"type"`
		Command        string   `json:"command"`
		CommandWindows string   `json:"commandWindows"`
		Args           []string `json:"args"`
	} `json:"hooks"`
}

// ReadHooks returns the hooks of a settings file. A missing or broken file has none.
func ReadHooks(path string) map[string][]HookGroup {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		Hooks map[string][]HookGroup `json:"hooks"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	return doc.Hooks
}

// CoversEveryTool reports whether the group runs for each tool. A gate behind a
// narrower matcher lets the other tools through with no decision.
func (g HookGroup) CoversEveryTool() bool {
	return g.Matcher == "" || g.Matcher == "*" || g.Matcher == ".*"
}

// IsGate reports whether handler i of the group runs the permission gate. Another
// duolog subcommand, such as a hook, decides nothing.
func (g HookGroup) IsGate(i int) bool {
	h := g.Hooks[i]
	for _, command := range []string{strings.Join(append([]string{h.Command}, h.Args...), " "), h.CommandWindows} {
		if IsOurCommand(command) && strings.Contains(command, "duolog gate") {
			return true
		}
	}
	return false
}

// IsOurs reports whether handler i of the group is duolog's.
func (g HookGroup) IsOurs(i int) bool {
	h := g.Hooks[i]
	return IsOurCommand(h.Command) || IsOurCommand(h.CommandWindows)
}

// HasGate reports whether the file has a permission gate that applies to every tool.
func HasGate(path string) bool {
	for _, group := range ReadHooks(path)["PreToolUse"] {
		for i := range group.Hooks {
			if group.IsGate(i) && group.CoversEveryTool() {
				return true
			}
		}
	}
	return false
}

// HasOurHook reports whether any event in the file runs duolog.
func HasOurHook(path string) bool {
	for _, groups := range ReadHooks(path) {
		for _, group := range groups {
			for i := range group.Hooks {
				if group.IsOurs(i) {
					return true
				}
			}
		}
	}
	return false
}

// ClaudeSettingsFiles lists the files Claude reads, in the order it applies them.
func ClaudeSettingsFiles(root, home string) []string {
	paths := []string{}
	if home != "" {
		paths = append(paths, filepath.Join(trusthome.Claude(home), "settings.json"))
	}
	if root != "" && root != home {
		paths = append(paths, filepath.Join(root, ".claude", "settings.json"), filepath.Join(root, ".claude", "settings.local.json"))
	}
	return paths
}

// PluginEnabled applies project and local plugin settings after user settings. An
// explicit project disable overrides an enabled user plugin.
func PluginEnabled(root, home string) bool {
	enabled := false
	for _, path := range ClaudeSettingsFiles(root, home) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Enabled map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(data, &doc) == nil {
			if value, ok := doc.Enabled[pluginKey]; ok {
				enabled = value
			}
		}
	}
	return enabled
}

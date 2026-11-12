package wiring

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/install"
)

// PluginHookDuplicate reports whether this hook process was started by the
// duolog Claude plugin while a settings file that Claude applies to the
// project runs the same duolog hook for the same event. The settings copy
// then does the work and the plugin's copy steps aside, so that nothing runs
// twice: a project that keeps duolog's hooks in its shared settings.json and
// a person who also has the plugin enabled get one gate and one notice.
//
// Claude gives CLAUDE_PLUGIN_ROOT only to a plugin's own hook processes, so
// a hook from a settings file never steps aside. Event is the Claude event,
// and verb the duolog words after the program, such as "gate" or
// "hook SessionStart".
func PluginHookDuplicate(environ []string, event, verb string) bool {
	plugin := envValue(environ, "CLAUDE_PLUGIN_ROOT")
	project := envValue(environ, "CLAUDE_PROJECT_DIR")
	if plugin == "" || project == "" || event == "" {
		return false
	}
	// Only duolog's own plugin, the one whose hooks run the gate.
	if !install.HasGate(filepath.Join(plugin, "hooks", "hooks.json")) {
		return false
	}
	home := envValue(environ, "HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	for _, path := range install.ClaudeSettingsFilesIn(project, home, environ) {
		for _, group := range install.ReadHooks(path)[event] {
			// A narrower matcher runs the settings copy for some tools or
			// sources only; the plugin's copy, which covers all, must stay.
			if !group.CoversEveryTool() {
				continue
			}
			for i := range group.Hooks {
				if runsVerb(group, i, verb) {
					return true
				}
			}
		}
	}
	return false
}

// runsVerb reports whether handler i of group runs duolog with verb, in the
// shell or the exec form.
func runsVerb(group install.HookGroup, i int, verb string) bool {
	h := group.Hooks[i]
	for _, command := range []string{strings.Join(append([]string{h.Command}, h.Args...), " "), h.CommandWindows} {
		if !install.IsOurCommand(command) && !group.IsOurs(i) {
			continue
		}
		if strings.Contains(" "+command+" ", " "+verb+" ") {
			return true
		}
	}
	return false
}

// envValue is the last value of key in environ, "" when it is not set.
func envValue(environ []string, key string) string {
	value := ""
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			value = v
		}
	}
	return value
}

package workerenv

import (
	"encoding/json"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
)

// ClaudeSettings is the --settings JSON of the free variant: it disables
// worker memory and lists state paths for an existing sandbox. It never
// enables the sandbox or changes the caller's extra settings. Fast mode is
// pinned off after the extra settings: it draws on usage credits even inside
// a subscription window, and --settings is the session-start layer that -p
// honours.
func ClaudeSettings(stateDir string, minimal bool, extra map[string]any) string {
	return ClaudeSettingsWithRoots(stateDir, minimal, extra, nil)
}

// ClaudeSettingsWithRoots is ClaudeSettings that also lists roots, the private
// usage stores, in sandbox.filesystem.denyRead. Only that list is merged: the
// caller's other sandbox keys, enabled among them, stay as given, so a sandbox
// that is off stays off and the denies protect only one already on.
func ClaudeSettingsWithRoots(stateDir string, minimal bool, extra map[string]any, roots []string) string {
	return renderClaudeSettings(stateDir, minimal, extra, roots, nil, true, 0)
}

// renderClaudeSettings makes the --settings JSON of either variant. Fast
// mode belongs to the variant, never to the caller's extras: pinned off in
// the free one, and left out of the paid one, so the person's own settings
// decide. A key is set or left out, never blanked. Roots join the sandbox
// denies in both variants. Plugins, when not nil, is the enabledPlugins map
// of the launch; it is laid over any the caller's extras hold. A compactWindow
// above zero is set as CLAUDE_CODE_AUTO_COMPACT_WINDOW in the env block, next
// to the env entries the caller's extras hold.
func renderClaudeSettings(stateDir string, minimal bool, extra map[string]any, roots []string, plugins map[string]bool, pinFast bool, compactWindow int) string {
	settings := map[string]any{}
	for key, value := range extra {
		if key != "sandbox" && key != "fastMode" {
			settings[key] = value
		}
	}
	setCompactWindow(settings, extra, compactWindow)
	if plugins != nil {
		merged := copyMap(extra["enabledPlugins"])
		for key, on := range plugins {
			merged[key] = on
		}
		settings["enabledPlugins"] = merged
	}
	if pinFast {
		settings["fastMode"] = false
	}
	settings["autoMemoryEnabled"] = false
	if minimal {
		settings["claudeMdExcludes"] = []string{"**/CLAUDE.md", "**/CLAUDE.local.md", "**/.claude/rules/**"}
	}
	sandbox := copyMap(extra["sandbox"])
	filesystem := copyMap(sandbox["filesystem"])
	denied := stringList(filesystem["denyRead"])
	if stateDir != "" {
		for _, name := range []string{
			"decisions.ndjson", "requests.ndjson", "bus.ndjson", "usage.ndjson", "coauthors.ndjson",
			"sessions.json", "cursors", "journal", "routing.learned.yaml",
		} {
			denied = append(denied, path.Join(pathutil.Slashes(stateDir), ".duolog", name))
		}
	}
	for _, root := range roots {
		if root != "" {
			denied = append(denied, path.Clean(pathutil.Slashes(root)))
		}
	}
	if len(denied) > 0 {
		filesystem["denyRead"] = unique(denied)
		sandbox["filesystem"] = filesystem
	}
	if len(sandbox) > 0 || extra["sandbox"] != nil {
		settings["sandbox"] = sandbox
	}
	data, _ := json.Marshal(settings)
	return string(data)
}

// setCompactWindow puts a window above zero in the env block of settings, next
// to the env entries the caller's extras hold.
func setCompactWindow(settings, extra map[string]any, window int) {
	if window <= 0 {
		return
	}
	env := copyMap(extra["env"])
	env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = strconv.Itoa(window)
	settings["env"] = env
}

// ownPlugin is the enabledPlugins key of duolog's own Claude plugin.
const ownPlugin = "duolog@duolog"

// workerPlugins is the enabledPlugins map of a worker or peer: every plugin the
// layers leave enabled, the last layer that names one deciding, is turned
// off, except duolog's own, which stays on. Each enabled plugin adds its
// skills to every request, and a worker needs only duolog's. A plugin the task
// needs (in.PluginNeeds) is left out of the map, so the person's setting for
// it holds; one they did not enable stays as it is. It is nil when
// workers.inherit_plugins is true or nothing is enabled. The layers are read
// facts; no settings file is touched.
func workerPlugins(in ProcessInput) map[string]bool {
	if in.Config.Workers.InheritPlugins {
		return nil
	}
	enabled := map[string]bool{}
	for _, layer := range in.Layers {
		for key, on := range layer.EnabledPlugins {
			enabled[key] = on
		}
	}
	out := map[string]bool{}
	for key, on := range enabled {
		name, _, _ := strings.Cut(key, "@")
		needed := slices.ContainsFunc(in.PluginNeeds, func(n string) bool { return n == key || n == name })
		switch {
		case needed:
			// The person's own setting holds.
		case key == ownPlugin && on:
			out[key] = true
		case on:
			out[key] = false
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// copyMap returns a shallow copy of v when it is a JSON object, else an empty
// map, so the caller's settings are never changed.
func copyMap(v any) map[string]any {
	out := map[string]any{}
	if m, ok := v.(map[string]any); ok {
		for key, value := range m {
			out[key] = value
		}
	}
	return out
}

// stringList reads a denyRead list as given in Go or decoded from JSON.
func stringList(v any) []string {
	switch list := v.(type) {
	case []string:
		return append([]string(nil), list...)
	case []any:
		var out []string
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// unique keeps the first of each path, in order.
func unique(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

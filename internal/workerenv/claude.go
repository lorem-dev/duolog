package workerenv

import (
	"encoding/json"
	"path"

	"github.com/lorem-dev/duolog/internal/pathutil"
)

// ClaudeSettings disables worker memory and lists state paths for an existing
// sandbox. It never enables the sandbox or changes the caller's extra settings.
// Fast mode is pinned off after the extra settings: it draws on usage credits
// even inside a subscription window, and --settings is the session-start layer
// that -p honours.
func ClaudeSettings(stateDir string, minimal bool, extra map[string]any) string {
	return ClaudeSettingsWithRoots(stateDir, minimal, extra, nil)
}

// ClaudeSettingsWithRoots is ClaudeSettings that also lists roots, the private
// usage stores, in sandbox.filesystem.denyRead. Only that list is merged: the
// caller's other sandbox keys, enabled among them, stay as given, so a sandbox
// that is off stays off and the denies protect only one already on.
func ClaudeSettingsWithRoots(stateDir string, minimal bool, extra map[string]any, roots []string) string {
	settings := map[string]any{}
	for key, value := range extra {
		if key != "sandbox" {
			settings[key] = value
		}
	}
	settings["fastMode"] = false
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

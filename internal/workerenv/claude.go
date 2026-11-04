package workerenv

import (
	"encoding/json"
	"path"

	"github.com/lorem-dev/duolog/internal/pathutil"
)

// ClaudeSettings disables worker memory and lists state paths for an existing
// sandbox. It never enables the sandbox or changes the caller's extra settings.
func ClaudeSettings(stateDir string, minimal bool, extra map[string]any) string {
	settings := map[string]any{}
	for key, value := range extra {
		if key != "sandbox" {
			settings[key] = value
		}
	}
	settings["autoMemoryEnabled"] = false
	if minimal {
		settings["claudeMdExcludes"] = []string{"**/CLAUDE.md", "**/CLAUDE.local.md", "**/.claude/rules/**"}
	}
	if stateDir != "" {
		var denied []string
		for _, name := range []string{
			"decisions.ndjson", "requests.ndjson", "bus.ndjson", "usage.ndjson", "coauthors.ndjson",
			"sessions.json", "cursors", "journal", "routing.learned.yaml",
		} {
			denied = append(denied, path.Join(pathutil.Slashes(stateDir), ".duolog", name))
		}
		settings["sandbox"] = map[string]any{"filesystem": map[string]any{"denyRead": denied}}
	}
	data, _ := json.Marshal(settings)
	return string(data)
}

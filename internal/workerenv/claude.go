package workerenv

import (
	"encoding/json"
	"path"

	"github.com/lorem-dev/duolog/internal/pathutil"
)

// ClaudeSettings is the --settings JSON of the free variant: it disables
// worker memory and lists state paths for an existing sandbox. It never
// enables the sandbox or changes the caller's extra settings. Fast mode is
// pinned off after the extra settings: it draws on usage credits even inside
// a subscription window, and --settings is the session-start layer that -p
// honours.
func ClaudeSettings(stateDir string, minimal bool, extra map[string]any) string {
	return renderClaudeSettings(stateDir, minimal, extra, true)
}

// renderClaudeSettings makes the --settings JSON of either variant. Fast
// mode belongs to the variant, never to the caller's extras: pinned off in
// the free one, and left out of the paid one, so the person's own settings
// decide. A key is set or left out, never blanked.
func renderClaudeSettings(stateDir string, minimal bool, extra map[string]any, pinFast bool) string {
	settings := map[string]any{}
	for key, value := range extra {
		if key != "sandbox" && key != "fastMode" {
			settings[key] = value
		}
	}
	if pinFast {
		settings["fastMode"] = false
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

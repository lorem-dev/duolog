package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// NativeAllow writes Claude's "don't ask again" rules into the project's
// .claude/settings.local.json.
type NativeAllow struct{}

func settingsPath(root string) string {
	return filepath.Join(root, ".claude", "settings.local.json")
}

// Allow adds Bash(prefix:*) or, for an exact command, Bash(command) to
// permissions.allow, keeping every other setting in the file.
func (NativeAllow) Allow(root, match string) (string, error) {
	prefix, anyArgs := strings.CutSuffix(match, " *")
	entry := "Bash(" + prefix + ")"
	if anyArgs {
		entry = "Bash(" + prefix + ":*)"
	}
	return addEntry(settingsPath(root), entry)
}

// AllowMCP allows one MCP tool or a whole server. Claude names the tool
// mcp__<server>__<tool>; a rule of mcp__<server> covers every tool of the
// server. Other globs have no Claude form.
func (NativeAllow) AllowMCP(root, server, tool string) (string, error) {
	entry := "mcp__" + claudeName(server)
	switch {
	case strings.ContainsAny(server, "*?[{"):
		entry = ""
	case tool == "*":
	case !strings.ContainsAny(tool, "*?[{"):
		entry += "__" + claudeName(tool)
	default:
		entry = ""
	}
	if entry == "" {
		return "no rule; Claude allows one MCP tool or a whole server, not a glob", nil
	}
	return addEntry(settingsPath(root), entry)
}

var claudeNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func claudeName(s string) string { return claudeNameChars.ReplaceAllString(s, "_") }

// addEntry adds entry to permissions.allow, keeping every other setting.
func addEntry(path, entry string) (string, error) {
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	permissions, _ := settings["permissions"].(map[string]any)
	if permissions == nil {
		permissions = map[string]any{}
	}
	allow, _ := permissions["allow"].([]any)
	for _, have := range allow {
		if have == entry {
			return "already " + entry, nil
		}
	}
	permissions["allow"] = append(allow, entry)
	settings["permissions"] = permissions
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return entry, os.WriteFile(path, append(out, '\n'), 0o644)
}

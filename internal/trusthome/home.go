// Package trusthome resolves native agent config directories without guessing
// the user's home from Unix-only environment variables.
package trusthome

import (
	"os"
	"path/filepath"
)

func Codex(home string) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}
func Claude(home string) string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".claude")
}

// ClaudeState is the legacy global state file (MCP names only are read).
func ClaudeState(home string) string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

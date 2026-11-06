// Package trusthome resolves native agent config directories without guessing
// the user's home from Unix-only environment variables.
package trusthome

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func Codex(home string) string {
	return CodexIn(home, os.Environ())
}
func Claude(home string) string {
	return ClaudeIn(home, os.Environ())
}

// ClaudeState is the legacy global state file (MCP names only are read).
func ClaudeState(home string) string {
	return ClaudeStateIn(home, os.Environ())
}

// CodexIn is the Codex config directory of a process whose environment is
// environ: its CODEX_HOME, else .codex under home.
func CodexIn(home string, environ []string) string {
	if dir := Getenv(environ, "CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}

// ClaudeIn is the Claude config directory of a process whose environment is
// environ: its CLAUDE_CONFIG_DIR, else .claude under home.
func ClaudeIn(home string, environ []string) string {
	if dir := Getenv(environ, "CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".claude")
}

// ClaudeStateIn is ClaudeState for a process whose environment is environ.
func ClaudeStateIn(home string, environ []string) string {
	if dir := Getenv(environ, "CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

// Getenv reads name from environ, NAME=value pairs, the last entry winning,
// with names compared without case on Windows; empty when it is not set.
func Getenv(environ []string, name string) string {
	value := ""
	for _, pair := range environ {
		key, v, _ := strings.Cut(pair, "=")
		if key == name || runtime.GOOS == "windows" && strings.EqualFold(key, name) {
			value = v
		}
	}
	return value
}

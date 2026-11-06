// Package trusthome resolves native agent config directories without guessing
// the user's home from Unix-only environment variables.
package trusthome

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/agent"
)

// ErrNoHome is the error of HomeIn for an environment that names no home
// folder: HOME, or USERPROFILE on Windows.
var ErrNoHome = agent.ErrNoHome

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

// HomeIn is the home folder of a process whose environment is environ: its
// HOME, or its USERPROFILE on Windows, as os.UserHomeDir reads duolog's own.
// An environment without one is ErrNoHome: the agent would fall back to a
// home, such as the account's passwd entry, that environ does not name, so
// nothing under it can be read for that process.
func HomeIn(environ []string) (string, error) {
	name := "HOME"
	if runtime.GOOS == "windows" {
		name = "USERPROFILE"
	}
	if home := Getenv(environ, name); home != "" {
		return home, nil
	}
	return "", fmt.Errorf("%w (%s)", ErrNoHome, name)
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

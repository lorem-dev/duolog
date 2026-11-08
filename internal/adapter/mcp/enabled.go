package mcp

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/project"
)

// Enabled reports whether a lead may get tools here, and when it may not,
// the one line that says why. Off under a worker (DUOLOG_DEPTH>=1), a peer
// (DUOLOG_PEER set), and outside a project with .duolog/config.yaml at
// project.StateRoot(cwd). It reads environ, not the process environment, so a
// test can pass its own.
func Enabled(environ []string, cwd string) (bool, string) {
	if depth, _ := strconv.Atoi(envValue(environ, "DUOLOG_DEPTH")); depth >= 1 {
		return false, "duolog tools are off here: this process runs inside a worker; only the lead has them"
	}
	if envValue(environ, "DUOLOG_PEER") != "" {
		return false, "duolog tools are off here: this process is a peer; only the lead has them"
	}
	root := project.StateRoot(cwd)
	if _, err := os.Stat(filepath.Join(root, ".duolog", "config.yaml")); err != nil {
		return false, "duolog tools are off here: no .duolog/config.yaml above " + cwd + "; run duolog install --scope project"
	}
	return true, ""
}

// envValue returns the last value of name in environ, as exec does.
func envValue(environ []string, name string) string {
	value := ""
	for _, pair := range environ {
		if k, v, ok := strings.Cut(pair, "="); ok && k == name {
			value = v
		}
	}
	return value
}

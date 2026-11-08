package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
)

// writeTaskFile renders t into <workRoot>/.duolog/tasks/<name>/task.md and
// returns the path relative to workRoot, slash-separated. The name is one
// folder name; a task that taskspec.Render refuses writes nothing.
func writeTaskFile(workRoot, name string, t mcp.Task) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("task folder name %q must be one plain name", name)
	}
	data, err := taskspec.Render(taskspec.Spec(t))
	if err != nil {
		return "", err
	}
	rel := path.Join(".duolog", "tasks", name, "task.md")
	full := filepath.Join(workRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		return "", err
	}
	return rel, nil
}

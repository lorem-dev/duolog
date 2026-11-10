package gate

import (
	"github.com/lorem-dev/duolog/internal/project"
	"path/filepath"
)

// Inherit the main checkout's local denies as well as the worker's. User
// settings remain first; importing another checkout can only narrow rules.
func inheritedLeadPaths(root, agent string, paths []string) []string {
	main := project.StateRoot(root)
	if main == root {
		return paths
	}
	result := []string{paths[0]}
	if agent == "claude" {
		result = append(result, filepath.Join(main, ".claude/settings.json"), filepath.Join(main, ".claude/settings.local.json"))
	} else {
		result = append(result, filepath.Join(main, ".codex/config.toml"))
	}
	return append(result, paths[1:]...)
}

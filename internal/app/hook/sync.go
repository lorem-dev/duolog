package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/sync"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

// syncConfig returns the sync settings, or false when this hook must not sync:
// a worker, an unknown agent, or a project without .duolog.
//
// It also returns the formats with the skills folders that the config sets.
func syncConfig(e Env) (agentsync.Formats, config.Sync, bool) {
	if worker(e) || !served(e) || e.Sync[e.Agent] == nil {
		return nil, config.Sync{}, false
	}
	if info, err := os.Stat(filepath.Join(e.Root, ".duolog")); err != nil || !info.IsDir() {
		return nil, config.Sync{}, false
	}
	cfg, err := configfile.Load(e.Root)
	if err != nil {
		return nil, config.Sync{}, false
	}
	// The files the sync writes are in the work root, which in a linked
	// worktree is not the root that holds the state, so its project config
	// names the skills folder.
	dirs, err := configfile.ProjectSkillsDirs(workRoot(e), configfile.BuiltinAgents())
	if err != nil {
		return nil, config.Sync{}, false
	}
	return e.Sync.Configured(dirs), cfg.Sync, true
}

// syncStart catches edits made outside the agent's file tools and new models.
func syncStart(e Env) string {
	formats, cfg, ok := syncConfig(e)
	if !ok {
		return ""
	}
	return agentsync.SessionStart(workRoot(e), formats, cfg, e.Models)
}

// syncTool rebuilds what the tool call touched. It reads the paths from a
// separate view of the hook input, so the parsing in input stays as it is.
func syncTool(e Env) string {
	formats, cfg, ok := syncConfig(e)
	if !ok {
		return ""
	}
	var lines []string
	for _, path := range touchedPaths(e.Input) {
		if line := agentsync.Hook(workRoot(e), formats, cfg, path, e.Models); line != "" && !contains(lines, line) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// touchedPaths lists the files a tool call names: file_path or path for file
// tools, and the "*** Update File:" lines of a Codex patch.
func touchedPaths(input []byte) []string {
	var h struct {
		Tool struct {
			FilePath string `json:"file_path"`
			Path     string `json:"path"`
			Command  any    `json:"command"`
			Patch    string `json:"patch"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(input, &h) != nil {
		return nil
	}
	var paths []string
	for _, p := range []string{h.Tool.FilePath, h.Tool.Path} {
		if p != "" {
			paths = append(paths, p)
		}
	}
	text := h.Tool.Patch
	if s, ok := h.Tool.Command.(string); ok {
		text += "\n" + s
	}
	paths = append(paths, policy.PatchPaths(text)...)
	return paths
}

func workRoot(e Env) string {
	if e.WorkRoot != "" {
		return e.WorkRoot
	}
	return e.Root
}

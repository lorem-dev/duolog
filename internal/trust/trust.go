// Package trust reads Codex folder and hook approval state without changing it.
package trust

import (
	"encoding/json"
	"fmt"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/trusthome"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type State struct {
	Trusted   bool
	Installed bool
}
type settings struct {
	Projects map[string]struct {
		TrustLevel string `toml:"trust_level"`
	} `toml:"projects"`
	Hooks struct {
		State map[string]struct {
			TrustedHash string `toml:"trusted_hash"`
		} `toml:"state"`
	} `toml:"hooks"`
}

func read() (settings, error) {
	var s settings
	home, err := os.UserHomeDir()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(filepath.Join(CodexHome(home), "config.toml"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = toml.Unmarshal(data, &s)
	return s, err
}
func Folder(agent, root string) (bool, error) {
	if agent == "claude" {
		return true, nil
	}
	s, err := read()
	if err != nil {
		return false, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return false, err
	}
	for path, settings := range s.Projects {
		if pathutil.Clean(pathutil.Resolved(path)) == pathutil.Clean(pathutil.Resolved(root)) && settings.TrustLevel == "trusted" {
			return true, nil
		}
	}
	return false, nil
}
func Hooks(agent, root string) (State, error) {
	if agent == "claude" {
		home, err := os.UserHomeDir()
		if err != nil {
			return State{}, err
		}
		installed := install.PluginEnabled(root, home)
		for _, path := range install.ClaudeSettingsFiles(root, home) {
			installed = installed || install.HasGate(path)
		}
		return State{Trusted: true, Installed: installed}, nil
	}
	s, err := read()
	if err != nil {
		return State{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return State{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return State{}, err
	}
	for _, path := range []string{filepath.Join(root, ".codex", "hooks.json"), filepath.Join(CodexHome(home), "hooks.json")} {
		state, err := hooksAt(path, s)
		if err != nil {
			return State{}, err
		}
		if state.Trusted {
			return state, nil
		}
	}
	return State{}, nil
}

func hooksAt(path string, s settings) (State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var definition struct {
		Hooks map[string][]install.HookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &definition); err != nil {
		return State{}, err
	}
	for i, group := range definition.Hooks["PreToolUse"] {
		if !group.CoversEveryTool() {
			continue
		}
		for j, hook := range group.Hooks {
			if hook.Type != "command" || !group.IsGate(j) {
				continue
			}
			// Codex's hash algorithm is unknown here. Changes after review cannot
			// be detected from this state; Codex itself will skip a changed hook.
			for approved, state := range s.Hooks.State {
				// Codex stores the snake_case event name; hooks.json uses PascalCase.
				for _, event := range []string{":pre_tool_use:", ":PreToolUse:"} {
					approvedPath, suffix, ok := strings.Cut(approved, event)
					if ok && suffix == fmt.Sprintf("%d:%d", i, j) && state.TrustedHash != "" &&
						pathutil.Clean(pathutil.Resolved(approvedPath)) == pathutil.Clean(pathutil.Resolved(path)) {
						return State{Trusted: true, Installed: true}, nil
					}
				}
			}
		}
	}
	return State{}, nil
}

// CodexHome honors the native CLI config directory override.
func CodexHome(home string) string {
	return trusthome.Codex(home)
}

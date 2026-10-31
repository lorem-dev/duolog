// Package trust reads Codex folder and hook approval state without changing it.
package trust

import (
	"encoding/json"
	"fmt"
	"github.com/lorem-dev/duolog/internal/install"
	"os"
	"path/filepath"

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
	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return s, err
		}
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
	return s.Projects[root].TrustLevel == "trusted", nil
}
func Hooks(agent, root string) (State, error) {
	if agent == "claude" {
		home, err := os.UserHomeDir()
		if err != nil {
			return State{}, err
		}
		installed := false
		for _, base := range []string{root, home} {
			data, err := os.ReadFile(filepath.Join(base, ".claude", "settings.json"))
			if err != nil {
				continue
			}
			var doc struct {
				Hooks map[string][]struct {
					Hooks []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"hooks"`
				Plugins map[string]bool `json:"enabledPlugins"`
			}
			if json.Unmarshal(data, &doc) != nil {
				continue
			}
			installed = installed || doc.Plugins["duolog@duolog"]
			for _, group := range doc.Hooks["PreToolUse"] {
				for _, h := range group.Hooks {
					installed = installed || install.IsOurCommand(h.Command)
				}
			}
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
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &definition); err != nil {
		return State{}, err
	}
	for i, group := range definition.Hooks["PreToolUse"] {
		for j, hook := range group.Hooks {
			if hook.Type != "command" || !install.IsOurCommand(hook.Command) {
				continue
			}
			key := fmt.Sprintf("%s:PreToolUse:%d:%d", path, i, j)
			// Codex's hash algorithm is unknown here. Changes after review cannot
			// be detected from this state; Codex itself will skip a changed hook.
			if s.Hooks.State[key].TrustedHash != "" {
				return State{Trusted: true, Installed: true}, nil
			}
		}
	}
	return State{}, nil
}

// CodexHome honors the native CLI config directory override.
func CodexHome(home string) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".codex")
}

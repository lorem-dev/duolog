// Package trust reads Codex folder and hook approval state without changing it.
package trust

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type State struct{ Trusted bool }
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
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
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
		return State{Trusted: true}, nil
	}
	s, err := read()
	if err != nil {
		return State{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return State{}, err
	}
	path := filepath.Join(root, ".codex", "hooks.json")
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
			if hook.Type != "command" || strings.TrimSpace(hook.Command) != "duolog gate" {
				continue
			}
			key := fmt.Sprintf("%s:PreToolUse:%d:%d", path, i, j)
			// Codex's hash algorithm is unknown here. Changes after review cannot
			// be detected from this state; Codex itself will skip a changed hook.
			if s.Hooks.State[key].TrustedHash != "" {
				return State{Trusted: true}, nil
			}
		}
	}
	return State{}, nil
}

// Package trust reads Codex folder and hook approval state without changing it.
package trust

import (
	"os"
	"path/filepath"

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
	key := filepath.Join(root, ".codex", "hooks.json") + ":PreToolUse:0:0"
	return State{Trusted: s.Hooks.State[key].TrustedHash != ""}, nil
}

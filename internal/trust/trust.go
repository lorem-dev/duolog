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
	// HooksOff is the first Claude settings file that sets disableAllHooks,
	// which turns every hook off, the gate included, whatever another layer
	// says; empty when none does.
	HooksOff string
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
	home, err := os.UserHomeDir()
	if err != nil {
		return settings{}, err
	}
	return readIn(home)
}

func readIn(home string) (settings, error) {
	var s settings
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
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	return FolderIn(home, root)
}

// FolderIn reports whether Codex trusts the folder root, reading the Codex
// config under home. Codex loads a project's .codex/ layer only then.
func FolderIn(home, root string) (bool, error) {
	s, err := readIn(home)
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
		state := State{Trusted: true, Installed: install.PluginEnabled(root, home)}
		for _, path := range install.ClaudeSettingsFiles(root, home) {
			state.Installed = state.Installed || install.HasGate(path)
			if state.HooksOff == "" && hooksOff(path) {
				state.HooksOff = path
			}
		}
		return state, nil
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

// hooksOff reports whether the Claude settings file sets disableAllHooks. A
// missing or broken file does not.
func hooksOff(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc struct {
		Off bool `json:"disableAllHooks"`
	}
	return json.Unmarshal(data, &doc) == nil && doc.Off
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

// ClaudeFolder reports whether Claude Code trusts the folder root, from the
// hasTrustDialogAccepted flag of root or one of its parents in Claude's user
// state under home. Only that flag is decoded; nothing else of the file is
// kept. known is false when the state is missing, unreadable or not JSON,
// and then trusted is false too.
func ClaudeFolder(home, root string) (trusted, known bool) {
	data, err := os.ReadFile(trusthome.ClaudeState(home))
	if err != nil {
		return false, false
	}
	var s struct {
		Projects map[string]struct {
			Accepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(data, &s) != nil {
		return false, false
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return false, false
	}
	accepted := map[string]bool{}
	for path, p := range s.Projects {
		if p.Accepted {
			accepted[pathutil.Clean(pathutil.Resolved(path))] = true
		}
	}
	dir := pathutil.Clean(pathutil.Resolved(root))
	for {
		if accepted[dir] {
			return true, true
		}
		parent := pathutil.Clean(filepath.Dir(dir))
		if parent == dir {
			return false, true
		}
		dir = parent
	}
}

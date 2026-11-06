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

	"github.com/pelletier/go-toml/v2"
)

type State struct {
	Trusted   bool
	Installed bool
	// HooksOff is the first Claude settings file that sets disableAllHooks,
	// which turns every hook off, the gate included, whatever another layer
	// says; empty when none does.
	HooksOff string
	// Unproven says why the gate that Trusted and Installed report cannot be
	// shown to run now: a Codex approval of a hook whose command is not
	// duolog's, or a Claude plugin that is enabled but not installed. Empty
	// when nothing is in doubt.
	Unproven string
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
		plugin := install.PluginEnabled(root, home)
		state := State{Trusted: true, Installed: plugin}
		settingsGate := false
		for _, path := range install.ClaudeSettingsFiles(root, home) {
			settingsGate = settingsGate || install.HasGate(path)
			if state.HooksOff == "" && hooksOff(path) {
				state.HooksOff = path
			}
		}
		state.Installed = state.Installed || settingsGate
		if plugin && !settingsGate && !install.PluginInstalled(root, home) {
			state.Unproven = "the duolog Claude plugin is enabled but not installed; run duolog install"
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
	var unproven State
	stale := install.StaleApprovals(home)
	for _, path := range []string{filepath.Join(root, ".codex", "hooks.json"), filepath.Join(CodexHome(home), "hooks.json")} {
		state, err := hooksAt(path, s, stale)
		if err != nil {
			return State{}, err
		}
		if state.Trusted && state.Unproven == "" {
			return state, nil
		}
		if state.Trusted && unproven.Unproven == "" {
			unproven = state
		}
	}
	return unproven, nil
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

// hooksAt finds an approved duolog gate in the Codex hooks file at path.
// stale maps approval keys to the hashes that duolog install made stale by
// changing the gate command (install.StaleApprovals).
func hooksAt(path string, s settings, stale map[string]string) (State, error) {
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
	posix, windows := install.CodexGateCommands("PreToolUse")
	var unproven State
	for i, group := range definition.Hooks["PreToolUse"] {
		if !group.CoversEveryTool() {
			continue
		}
		for j, hook := range group.Hooks {
			if hook.Type != "command" || !group.IsGate(j) {
				continue
			}
			for approved, state := range s.Hooks.State {
				approvedPath, suffix, ok := install.ApprovalPath(approved)
				if !ok || suffix != fmt.Sprintf("%d:%d", i, j) || state.TrustedHash == "" ||
					pathutil.Clean(pathutil.Resolved(approvedPath)) != pathutil.Clean(pathutil.Resolved(path)) {
					continue
				}
				// Codex's hash algorithm is unknown here, and Codex skips a
				// hook whose command changed after approval. duolog installs
				// only its own command, so any other command at an approved
				// position cannot be shown to be the approved one; nor can
				// duolog's own command when install rewrote it after the
				// approval and Codex still holds the hash it had then.
				reason := ""
				switch {
				case hook.Command != posix || hook.CommandWindows != windows || len(hook.Args) != 0:
					reason = "the approved Codex gate hook in " + path +
						" is not duolog's gate command, and Codex skips a hook changed after approval; run duolog install and approve it in /hooks"
				case stale[approved] == state.TrustedHash:
					reason = "duolog install changed the Codex gate hook in " + path +
						" after Codex approved it, and Codex skips a hook changed after approval; approve it again in /hooks"
				default:
					return State{Trusted: true, Installed: true}, nil
				}
				if unproven.Unproven == "" {
					unproven = State{Trusted: true, Installed: true, Unproven: reason}
				}
			}
		}
	}
	return unproven, nil
}

// CodexHome honors the native CLI config directory override.
func CodexHome(home string) string {
	return trusthome.Codex(home)
}

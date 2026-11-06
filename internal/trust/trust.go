// Package trust reads Codex folder and hook approval state without changing it.
package trust

import (
	"encoding/json"
	"fmt"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/install"
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

// readEnv reads config.toml in the Codex home of a process whose environment
// is environ.
func readEnv(home string, environ []string) (settings, error) {
	var s settings
	data, err := os.ReadFile(filepath.Join(trusthome.CodexIn(home, environ), "config.toml"))
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
	return FolderEnv(agent, root, os.Environ())
}

// FolderEnv is Folder for an agent process whose environment is environ: the
// home folder and the Codex home it reads are the ones that environment
// selects. An environment without a home folder is trusthome.ErrNoHome.
func FolderEnv(agent, root string, environ []string) (bool, error) {
	if agent == "claude" {
		return true, nil
	}
	home, err := trusthome.HomeIn(environ)
	if err != nil {
		return false, err
	}
	return folderIn(home, root, environ)
}

// FolderInEnv is FolderIn for a process whose environment is environ: its
// CODEX_HOME, else .codex under home.
func FolderInEnv(home, root string, environ []string) (bool, error) {
	return folderIn(home, root, environ)
}

// FolderIn reports whether Codex trusts the folder root, reading the Codex
// config under home. Codex loads a project's .codex/ layer only then.
func FolderIn(home, root string) (bool, error) {
	return folderIn(home, root, os.Environ())
}

func folderIn(home, root string, environ []string) (bool, error) {
	s, err := readEnv(home, environ)
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
	return HooksEnv(agent, root, os.Environ())
}

// HooksEnv is Hooks for an agent process whose environment is environ: the
// home folder, the Claude config directory and the Codex home, with their
// settings, plugins and approvals, are the ones that environment selects, not
// duolog's own. An environment without a home folder is
// trusthome.ErrNoHome.
func HooksEnv(agent, root string, environ []string) (State, error) {
	home, err := trusthome.HomeIn(environ)
	if err != nil {
		return State{}, err
	}
	if agent == "claude" {
		plugin := install.PluginEnabledIn(root, home, environ)
		state := State{Trusted: true, Installed: plugin}
		settingsGate := false
		for _, path := range install.ClaudeSettingsFilesIn(root, home, environ) {
			settingsGate = settingsGate || install.HasGate(path)
			if state.HooksOff == "" && hooksOff(path) {
				state.HooksOff = path
			}
		}
		state.Installed = state.Installed || settingsGate
		if plugin && !settingsGate && !install.PluginInstalledIn(root, home, environ) {
			state.Unproven = "the duolog Claude plugin is enabled but not installed; run duolog install"
		}
		return state, nil
	}
	s, err := readEnv(home, environ)
	if err != nil {
		return State{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return State{}, err
	}
	var unproven State
	stale := install.StaleApprovalsIn(home, environ)
	for _, path := range []string{filepath.Join(root, ".codex", "hooks.json"), filepath.Join(trusthome.CodexIn(home, environ), "hooks.json")} {
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

// HeldGateProblem says why a Claude process whose environment is environ
// cannot be shown to run duolog's gate before each tool call, for a launch
// that only the gate keeps read-only; "" when it can. Every PreToolUse hook
// that names the gate, in the settings files and the enabled plugin, must be
// the command duolog install writes (install.HookGroup.GateProgram), run by
// gate, the duolog on the launch's PATH, and one of them must cover every
// tool. A hook that only names the gate, such as one in a shell comment, or
// that runs another or a missing duolog, proves nothing.
func HeldGateProblem(root string, environ []string, gate string) string {
	home, err := trusthome.HomeIn(environ)
	if err != nil {
		return err.Error()
	}
	files := install.ClaudeSettingsFilesIn(root, home, environ)
	if install.PluginEnabledIn(root, home, environ) {
		files = append(files, install.PluginGateFilesIn(root, home, environ)...)
	}
	proven := false
	for _, path := range files {
		for _, group := range install.ReadHooks(path)["PreToolUse"] {
			for i := range group.Hooks {
				if !group.IsGate(i) {
					continue
				}
				if problem := heldHookProblem(path, group, i, gate); problem != "" {
					return problem
				}
				proven = proven || group.CoversEveryTool()
			}
		}
	}
	if !proven {
		return "no Claude PreToolUse hook runs duolog's gate for every tool; run duolog install"
	}
	return ""
}

// heldHookProblem says why handler i of group, a hook in the file at path
// that names the gate, does not prove the gate runs; "" when it does.
func heldHookProblem(path string, group install.HookGroup, i int, gate string) string {
	program, ok := group.GateProgram(i, "claude", "PreToolUse")
	why := "it is not the gate command duolog install writes"
	if ok && !sameProgram(program, gate) {
		ok, why = false, "it runs "+program+", not the duolog on the worker PATH ("+gate+")"
	}
	if ok {
		return ""
	}
	h := group.Hooks[i]
	text := strings.Join(append([]string{h.Command}, h.Args...), " ")
	return fmt.Sprintf("run duolog install: the Claude PreToolUse hook in %s does not prove the gate runs, since %s (hook %q)", path, why, text)
}

// sameProgram reports whether a hook's program is gate: a bare name, which
// the hook's shell finds on the PATH where gate was found, or an absolute
// path to the same existing file.
func sameProgram(program, gate string) bool {
	if !strings.ContainsAny(program, `/\`) {
		return true
	}
	if !filepath.IsAbs(program) {
		return false
	}
	info, err := os.Stat(program)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if filepath.Clean(program) == filepath.Clean(gate) {
		return true
	}
	other, err := os.Stat(gate)
	return err == nil && os.SameFile(info, other)
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
				var reason string
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

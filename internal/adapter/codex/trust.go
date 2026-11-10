package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// Trust reads Codex's folder and hook approvals without changing them.
type Trust struct{}

// codexSettings is the part of config.toml that hook approval lives in.
type codexSettings struct {
	Hooks struct {
		State map[string]struct {
			TrustedHash string `toml:"trusted_hash"`
			Enabled     *bool  `toml:"enabled"`
		} `toml:"state"`
	} `toml:"hooks"`
}

// readSettings reads config.toml in the Codex home that environ selects.
func readSettings(environ []string) (codexSettings, error) {
	var s codexSettings
	dir, err := kit.Home(environ, "CODEX_HOME", ".codex")
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = toml.Unmarshal(data, &s)
	return s, err
}

// Folder reports whether Codex trusts the folder root, reading the config of
// the home that environ selects. Codex loads a project's .codex/ layer only
// then. An environment without a home folder is trusthome.ErrNoHome.
func (Trust) Folder(root string, environ []string) (bool, error) {
	home, err := trusthome.HomeIn(environ)
	if err != nil {
		return false, err
	}
	return trust.FolderInEnv(home, root, environ)
}

// Hooks reports the approval state of duolog's gate hook for a Codex process
// whose environment is environ: the home folder and the Codex home, with
// their hooks files and approvals, are the ones that environment selects. An
// environment without a home folder is trusthome.ErrNoHome.
func (Trust) Hooks(root string, environ []string) (agent.TrustState, error) {
	home, err := trusthome.HomeIn(environ)
	if err != nil {
		return agent.TrustState{}, err
	}
	s, err := readSettings(environ)
	if err != nil {
		return agent.TrustState{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return agent.TrustState{}, err
	}
	codexHome, err := kit.Home(environ, "CODEX_HOME", ".codex")
	if err != nil {
		return agent.TrustState{}, err
	}
	var unproven agent.TrustState
	stale := install.StaleApprovalsIn(home, environ)
	for _, path := range []string{filepath.Join(root, ".codex", "hooks.json"), filepath.Join(codexHome, "hooks.json")} {
		state, err := hooksAt(path, s, stale)
		if err != nil {
			return agent.TrustState{}, err
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

// GateProblem says why the duolog gate cannot be shown to run for Codex, or
// returns "" when it can. folder is whether Codex trusts the working folder.
func (Trust) GateProblem(s agent.TrustState, folder bool) string {
	if !s.Trusted {
		return "Codex hooks are not approved; open /hooks"
	}
	if !folder {
		return "Codex does not trust this folder"
	}
	return s.Unproven
}

// HeldGateProblem is "": only a Claude launch is held read-only by the gate
// alone.
func (Trust) HeldGateProblem(string, []string, string) string { return "" }

// hooksAt finds an approved duolog gate in the Codex hooks file at path.
// stale maps approval keys to the hashes that duolog install made stale by
// changing the gate command (install.StaleApprovals).
func hooksAt(path string, s codexSettings, stale map[string]string) (agent.TrustState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return agent.TrustState{}, nil
	}
	if err != nil {
		return agent.TrustState{}, err
	}
	var definition struct {
		Hooks map[string][]install.HookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(data, &definition); err != nil {
		return agent.TrustState{}, err
	}
	// The dev launcher form counts only in a checkout that has duolog's dev
	// launcher, as duolog's own repository does.
	dev := install.IsDevLauncher(filepath.Join(filepath.Dir(filepath.Dir(path)), filepath.FromSlash(devLaunchers[0])))
	var unproven agent.TrustState
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
				if !ok || suffix != fmt.Sprintf("%d:%d", i, j) || !activeCodexApproval(state.TrustedHash, state.Enabled) ||
					pathutil.Clean(pathutil.Resolved(approvedPath)) != pathutil.Clean(pathutil.Resolved(path)) {
					continue
				}
				problem := codexApprovalProblem(path, state.TrustedHash, stale[approved])
				var reason string
				switch {
				case !ownCommands("PreToolUse", hook.Command, hook.CommandWindows, dev) || len(hook.Args) != 0:
					reason = "the approved Codex gate hook in " + path +
						" is not duolog's gate command, and Codex skips a hook changed after approval; run duolog install and approve it in /hooks"
				case problem != "":
					reason = problem
				default:
					return agent.TrustState{Trusted: true, Installed: true}, nil
				}
				if unproven.Unproven == "" {
					unproven = agent.TrustState{Trusted: true, Installed: true, Unproven: reason}
				}
			}
		}
	}
	return unproven, nil
}

func activeCodexApproval(hash string, enabled *bool) bool {
	return hash != "" && (enabled == nil || *enabled)
}

func codexApprovalProblem(path, hash, staleHash string) string {
	if staleHash == hash {
		return "duolog install changed the Codex gate hook in " + path + " after Codex approved it, and Codex skips a hook changed after approval; approve it again in /hooks"
	}
	// duolog does not compute Codex's approval hash: its form may change
	// between Codex versions, and a wrong hash would refuse every worker.
	return ""
}

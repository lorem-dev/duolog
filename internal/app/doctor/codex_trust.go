package doctor

import (
	"fmt"
	"os"

	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// FolderTrust is what InstallTrustNote reads of an agent's trust.
type FolderTrust interface {
	Folder(root string, env []string) (bool, error)
	Hooks(root string, env []string) (coreagent.TrustState, error)
}

// InstallTrustNote is the note project install prints when the project enables
// Codex and Codex does not trust workRoot or has not approved its hooks, and ""
// otherwise. trustOf returns an agent's trust, or nil when it has none.
func InstallTrustNote(cfg *config.Config, trustOf func(agent string) FolderTrust, workRoot string) string {
	if !cfg.AgentEnabled(codexName) {
		return ""
	}
	var folder bool
	var hooks coreagent.TrustState
	if t := trustOf(codexName); t != nil {
		folder, _ = t.Folder(workRoot, os.Environ())
		hooks, _ = t.Hooks(workRoot, os.Environ())
	}
	approved := hooks.Trusted && hooks.Unproven == ""
	if folder && approved {
		return ""
	}
	return coreagent.CodexTrustNote(workRoot, approved, folder)
}

// codexTrustFindings reports Codex folder trust and hook approval for
// workRoot, or root when workRoot is empty. A project that enables Codex and
// lacks either gets a warning that says what to do; otherwise it is info.
func codexTrustFindings(d Deps, cfg *config.Config, workRoot, root string) []Finding {
	if workRoot == "" {
		workRoot = root
	}
	var out []Finding
	var folder bool
	var hooks coreagent.TrustState
	if t := d.Agents[codexName].Trust; t != nil {
		var err error
		if folder, err = t.Folder(workRoot, os.Environ()); err != nil {
			out = append(out, Finding{"warning", "Codex folder trust: " + err.Error()})
		}
		if hooks, err = t.Hooks(workRoot, os.Environ()); err != nil {
			out = append(out, Finding{"warning", "Codex hook trust: " + err.Error()})
		}
	}
	approved := hooks.Trusted && hooks.Unproven == ""
	if !cfg.AgentEnabled(codexName) || (approved && folder) {
		return append(out, Finding{"info", fmt.Sprintf("Codex hooks approved=%t folder trusted=%t", hooks.Trusted, folder)})
	}
	note := coreagent.CodexTrustNote(workRoot, approved, folder)
	if hooks.Unproven != "" {
		note += "; " + hooks.Unproven
	}
	return append(out, Finding{"warning", note})
}

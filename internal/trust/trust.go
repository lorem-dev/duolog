// Package trust reads Codex's folder trust without changing it. Each agent's
// hook and folder state is read by that agent's adapter (the Trust port); what
// stays here is CodexHome, which the security boundary calls, and the folder
// read that capability still uses.
package trust

import (
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// State is the hook state of one agent's process; agent.TrustState is its
// home.
type State = agent.TrustState

type settings struct {
	Projects map[string]struct {
		TrustLevel string `toml:"trust_level"`
	} `toml:"projects"`
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

// FolderInEnv reports whether Codex trusts the folder root for a process whose
// environment is environ: its CODEX_HOME, else .codex under home. Codex loads
// a project's .codex/ layer only then.
func FolderInEnv(home, root string, environ []string) (bool, error) {
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

// CodexHome honors the native CLI config directory override.
func CodexHome(home string) string {
	return trusthome.Codex(home)
}

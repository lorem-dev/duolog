package codex

import (
	"errors"
	"fmt"

	"github.com/lorem-dev/duolog/internal/core/agent"
)

// Tree guards a calibration tree or judge slot for a Codex worker.
type Tree struct{}

// Prepare does nothing: Codex reads the project's .codex/hooks.json, which the
// calibration tree keeps in its slot for the person to approve once, and duolog
// never edits the Codex config.
func (Tree) Prepare(string, []string) error { return nil }

// Check says whether Codex trusts dir and has approved its hooks; duolog only
// reads the Codex config, and the person grants both in Codex. environ is the
// environment the worker gets (workerenv.Environ): the approvals and trusted
// folders read are those it selects, not duolog's own.
func (Tree) Check(dir string, environ []string) error {
	folder, err := Trust{}.Folder(dir, environ)
	var hooks agent.TrustState
	if err == nil {
		hooks, err = Trust{}.Hooks(dir, environ)
	}
	if errors.Is(err, agent.ErrNoHome) {
		return fmt.Errorf("the codex worker's environment names no home folder, so duolog cannot read the settings it loads in the calibration tree %s, and no cell runs there: add HOME (USERPROFILE on Windows) to env_passthrough", dir)
	}
	if err != nil {
		return err
	}
	if folder && hooks.Trusted {
		return nil
	}
	return fmt.Errorf("Codex must trust the calibration tree %s and its hooks need approval: open Codex in %s once, trust the folder and approve its hooks in /hooks", dir, dir) //nolint:staticcheck // ST1005: Codex is a product name
}

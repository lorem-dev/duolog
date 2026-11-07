package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/core/agent"
)

// localSettings is the personal Claude settings file, where a calibration tree
// gets duolog's gate when nothing else gives Claude one there.
const localSettings = ".claude/settings.local.json"

// gateTimeout is the gate's hook timeout in seconds, as duolog install writes
// it.
const gateTimeout = 150

// Tree guards a calibration tree or judge slot for a Claude worker.
type Tree struct{}

// Prepare writes duolog's Claude hooks into dir's local settings unless Claude
// already runs the gate in dir for a worker whose environment environ builds.
func (Tree) Prepare(dir string, environ func() ([]string, error)) error {
	env, err := environ()
	if err != nil {
		return err
	}
	state, err := Trust{}.Hooks(dir, env)
	if errors.Is(err, agent.ErrNoHome) {
		// The worker's own settings cannot be read, so they cannot be shown
		// to run the gate: the tree gets its own, and delegate refuses a cell
		// whose gate it cannot show.
		state, err = agent.TrustState{}, nil
	}
	if err != nil || state.Installed {
		return err
	}
	hooks, err := HooksJSON(gateTimeout)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, filepath.FromSlash(localSettings))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	return os.WriteFile(target, hooks, 0o600)
}

// Check says whether the duolog gate is active for Claude in dir: installed,
// and no settings layer Claude loads there sets disableAllHooks. Claude needs
// no folder trust. environ is the environment the worker gets
// (workerenv.Environ): the settings read are those it selects, not duolog's
// own.
func (Tree) Check(dir string, environ []string) error {
	_, err := Trust{}.Folder(dir, environ)
	var hooks agent.TrustState
	if err == nil {
		hooks, err = Trust{}.Hooks(dir, environ)
	}
	if errors.Is(err, agent.ErrNoHome) {
		return fmt.Errorf("the claude worker's environment names no home folder, so duolog cannot read the settings it loads in the calibration tree %s, and no cell runs there: add HOME (USERPROFILE on Windows) to env_passthrough", dir)
	}
	if err != nil {
		return err
	}
	if hooks.HooksOff != "" {
		return fmt.Errorf("Claude would run without the duolog gate in the calibration tree %s, so no cell runs there: %s sets disableAllHooks; remove it from that file, or from the commit's .claude settings with a branch that lacks it", dir, hooks.HooksOff) //nolint:staticcheck // ST1005: Claude is a product name
	}
	if hooks.Installed {
		return nil
	}
	return fmt.Errorf("Claude has no duolog gate in the calibration tree %s, so no cell runs there: duolog writes it to %s when no other settings run it; check that the folder is writable", dir, localSettings) //nolint:staticcheck // ST1005: Claude is a product name
}

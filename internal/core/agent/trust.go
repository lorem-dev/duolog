package agent

import "errors"

// ErrNoHome is the error of an environment that names no home folder: HOME,
// or USERPROFILE on Windows. trusthome.ErrNoHome is this value, so a Trust
// port and the code that asks it share one sentinel.
var ErrNoHome = errors.New("the environment names no home folder")

// TrustState is what an agent's settings and approvals say about duolog's
// hooks for one folder and one process environment.
type TrustState struct {
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

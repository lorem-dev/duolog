package peer

import corescreen "github.com/lorem-dev/duolog/internal/core/screen"

// The screen reading lives in core/screen. These names stay for cmd/duolog and
// for the code of this package that names them.
type (
	// State is what a peer session is doing, read from a bounded screen sample.
	State = corescreen.State
	// Option is one choice of a question.
	Option = corescreen.Option
	// Screen is the reading of one sample.
	Screen = corescreen.Screen
	// RunHereError carries the line that tells the lead where to do the task now.
	RunHereError = corescreen.RunHereError
)

const (
	Starting    = corescreen.Starting
	Updating    = corescreen.Updating
	Question    = corescreen.Question
	Ready       = corescreen.Ready
	Interrupted = corescreen.Interrupted
	Limited     = corescreen.Limited
	Exited      = corescreen.Exited
	Closed      = corescreen.Closed
	Gone        = corescreen.Gone
)

// Parse reads a captured screen for the named agent.
var Parse = corescreen.Parse

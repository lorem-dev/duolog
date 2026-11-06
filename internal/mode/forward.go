// Package mode forwards to internal/core/mode for the security boundary,
// whose files keep their imports until they move.
package mode

import coremode "github.com/lorem-dev/duolog/internal/core/mode"

type Mode = coremode.Mode

const (
	Plan  = coremode.Plan
	Ask   = coremode.Ask
	Edits = coremode.Edits
	Full  = coremode.Full
)

var (
	Parse = coremode.Parse
	Lower = coremode.Lower
)

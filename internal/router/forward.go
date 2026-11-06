// Package router forwards to internal/core/route for the security boundary,
// whose files keep their imports until they move.
package router

import "github.com/lorem-dev/duolog/internal/core/route"

type (
	PaceWindow = route.PaceWindow
	AgentPace  = route.AgentPace
	Balance    = route.Balance
)

var (
	Elapsed = route.Elapsed
	Ahead   = route.Ahead
)

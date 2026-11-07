package peer

import (
	"github.com/lorem-dev/duolog/internal/adapter/session"
)

// paneOf always targets the pane's recorded session. The lead's session is
// used only for legacy entries that predate session recording.
func (e *env) paneOf(entry session.Entry) PaneID {
	return e.paneIn(entry.RexSession, entry.Block)
}

// paneIn is block in session, or in the lead's session when session is empty.
func (e *env) paneIn(session, block string) PaneID {
	if session == "" {
		session = e.leadSession
	}
	return PaneID{Session: session, Block: block}
}

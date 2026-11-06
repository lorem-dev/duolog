package peer

import (
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/session"
)

// clientFor always targets the pane's recorded session. The lead's Rex
// session is used only for legacy entries that predate session recording.
func (e *env) clientFor(entry session.Entry) rex.Client {
	client := e.rex
	if entry.RexSession != "" {
		client.Session = entry.RexSession
	}
	client.Anchor = ""
	return client
}

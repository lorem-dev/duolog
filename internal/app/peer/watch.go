package peer

import (
	"context"

	"github.com/lorem-dev/duolog/internal/adapter/session"
)

// watchMarked watches a sent task as watch does, and marks the peer as
// watched meanwhile. The peer's Stop hook then leaves the turn's end to this
// wait, which reports it, and tells the lead itself only when no wait runs.
func (e *env) watchMarked(ctx context.Context, entry session.Entry) (Entry, error) {
	release, err := e.reg.Watch(entry.ID)
	if err != nil {
		return Entry{ID: entry.ID, State: Starting}, err
	}
	defer release()
	return e.watch(ctx, entry, false)
}

package peer

import (
	"context"

	"github.com/lorem-dev/duolog/internal/rex"
)

// ListedPeer is a snapshot of a lead's peer in the current Rex session.
type ListedPeer struct {
	ID    string `json:"id"`
	State State  `json:"state"`
}

// List observes states without sending keys, answering questions or changing
// session records. Rebuild uses this to leave busy and asking peers alone.
func List(ctx context.Context) ([]ListedPeer, error) {
	e, err := setup(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := e.reg.Entries()
	if err != nil {
		return nil, err
	}
	peers := []ListedPeer{}
	for _, entry := range entries {
		if entry.Block == "" || entry.Closed || entry.Lead != e.leadID || entry.RexSession != "" && entry.RexSession != e.rex.Session {
			continue
		}
		presence, err := e.rex.Probe(ctx, entry.Block)
		if err != nil {
			return nil, err
		}
		state := Closed
		switch presence {
		case rex.Exited:
			state = Exited
		case rex.Running:
			screen, err := e.screen(ctx, entry)
			if rex.IsNotFound(err) {
				break
			}
			if err != nil {
				return nil, err
			}
			state = screen.State
		}
		peers = append(peers, ListedPeer{ID: entry.ID, State: state})
	}
	return peers, nil
}

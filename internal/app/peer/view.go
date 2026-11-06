package peer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/lorem-dev/duolog/internal/rex"
)

// Viewed is the outcome of moving one Rex client to a peer's tab.
type Viewed struct{ ID, Tab, Client string }

// Line is the one line `peer view` prints. It never holds screen text. The
// person now looks at the peer's pane, where a typed command would reach the
// peer, so the way back is the Rex action, not a command.
func (v Viewed) Line() string {
	return v.ID + " shown on client " + v.Client + ": " + v.Tab + "; to return, use Previous Session in Rex"
}

// View asks Rex to show a peer's existing tab on one client. It is passive:
// it reads the record, probes the pane, lists clients and moves focus. It
// presses no key, opens nothing and writes no record. Focus is interactive,
// so the person can type into the peer; duolog cannot filter that.
func View(ctx context.Context, id, client string) (Viewed, error) {
	e, err := setupViewer(ctx)
	if err != nil {
		return Viewed{}, err
	}
	entry, err := e.find(id)
	if err != nil {
		return Viewed{}, err
	}
	if entry.Closed {
		return Viewed{}, fmt.Errorf("peer %s is closed", entry.ID)
	}
	rc := e.clientFor(entry)
	presence, err := rc.Probe(ctx, entry.Block)
	if err != nil {
		return Viewed{}, err
	}
	if presence == rex.Gone {
		return Viewed{}, fmt.Errorf("peer %s: pane is gone", entry.ID)
	}
	if client == "" {
		clients, err := e.rex.Clients(ctx)
		if err != nil {
			return Viewed{}, err
		}
		if client, err = pickClient(clients, "", os.Getenv("REX_SESSION")); err != nil {
			return Viewed{}, err
		}
	}
	if err := rc.Focus(ctx, client, entry.Block); err != nil {
		return Viewed{}, err
	}
	return Viewed{ID: entry.ID, Tab: tabLabel(entry.Agent, entry.Role), Client: client}, nil
}

// pickClient chooses the client to move. An explicit id wins. Otherwise it is
// the one app client attached to the lead's session. Command line clients
// never count, and zero or several matches refuse.
func pickClient(clients []rex.ClientInfo, explicit, leadSession string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	var found []string
	for _, c := range clients {
		if c.Kind != "cli" && c.ID != "" && slices.Contains(c.Sessions, leadSession) {
			found = append(found, c.ID)
		}
	}
	if len(found) != 1 {
		return "", errors.New("no single Rex client shows this session; run rex client ls and pass --client <id>")
	}
	return found[0], nil
}

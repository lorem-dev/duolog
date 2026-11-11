package peer

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/store"
)

// ErrNoRex says Rex cannot be reached from here, so no tab opens.
var ErrNoRex = errors.New("no Rex here")

// WatchTab opens a tab that runs argv, a `duolog watch <run>` command, in the
// project's peer session, and moves one Rex client to it as View does. The
// tab holds no peer: nothing is recorded, and the program reads no key, so
// what the person types goes nowhere. It returns ErrNoRex when Rex cannot be
// reached. The client is chosen before anything opens.
func WatchTab(ctx context.Context, run string, argv []string, client string) (Viewed, error) {
	opts, err := callerOptions(ctx)
	if err != nil {
		return Viewed{}, err
	}
	if os.Getenv("REX_SESSION") == "" {
		return Viewed{}, ErrNoRex
	}
	if _, ok := opts.Deps.Pane.Lead(); !ok {
		return Viewed{}, ErrNoRex
	}
	e, err := setupViewer(ctx)
	if err != nil {
		return Viewed{}, err
	}
	if client == "" {
		clients, err := e.panes.Clients(ctx)
		if err != nil {
			return Viewed{}, err
		}
		if client, err = pickClient(clients, "", os.Getenv("REX_SESSION")); err != nil {
			return Viewed{}, err
		}
	}
	label := "duolog | watch " + run
	unlock, err := store.LockFile(filepath.Join(e.dir, "peer-open.lock"))
	if err != nil {
		return Viewed{}, err
	}
	block, session, err := e.openProjectPeer(ctx, label, argv)
	if unlockErr := unlock(); err == nil {
		err = unlockErr
	}
	if err != nil {
		return Viewed{}, err
	}
	if err := e.panes.Focus(ctx, PaneID{Session: session, Block: block}, client); err != nil {
		return Viewed{}, err
	}
	return Viewed{ID: run, Tab: label, Client: client}, nil
}

// CloseWatchTab closes the Rex pane the caller runs in, the tab WatchTab
// opened. It closes {REX_SESSION, REX_BLOCK} only when REX_SESSION is the
// project's peer session, so a person who runs it in their own pane closes
// nothing.
func CloseWatchTab(ctx context.Context) error {
	e, err := setupViewer(ctx)
	if err != nil {
		return err
	}
	session, block := os.Getenv("REX_SESSION"), os.Getenv("REX_BLOCK")
	peerSession, err := e.reg.PeerRexSession()
	if err != nil {
		return err
	}
	if block == "" || peerSession == "" || session != peerSession {
		return errors.New("this pane is not a watch tab of the project's peer session")
	}
	return e.panes.Close(ctx, PaneID{Session: session, Block: block})
}

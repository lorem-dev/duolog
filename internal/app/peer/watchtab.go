package peer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	corepane "github.com/lorem-dev/duolog/internal/core/pane"
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
	block, session, err := e.watchTabOf(ctx, run, label, argv)
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

// watchTab is the tab WatchTab last opened for a run.
type watchTab struct {
	Session string `json:"session"`
	Block   string `json:"block"`
}

// watchTabPath is the record of a run's watch tab, or "" for a run id that is
// not a plain file name.
func (e *env) watchTabPath(run string) string {
	if run == "" || run != filepath.Base(run) || strings.HasPrefix(run, ".") {
		return ""
	}
	return filepath.Join(e.dir, "watch-tabs", run+".json")
}

// watchTabOf is the tab that shows run: the one an earlier watch opened while
// it still runs, else a new one in the project's peer session, recorded for
// the next watch. A record that cannot be read or written only costs a second
// tab. The caller holds the peer-open lock.
func (e *env) watchTabOf(ctx context.Context, run, label string, argv []string) (block, session string, err error) {
	path := e.watchTabPath(run)
	if path != "" {
		var tab watchTab
		if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &tab) == nil && tab.Block != "" && tab.Session != "" {
			if p, err := e.panes.Probe(ctx, PaneID{Session: tab.Session, Block: tab.Block}); err == nil && p == corepane.Running {
				return tab.Block, tab.Session, nil
			}
		}
	}
	block, session, err = e.openProjectPeer(ctx, label, argv)
	if err != nil || path == "" {
		return block, session, err
	}
	if data, err := json.Marshal(watchTab{Session: session, Block: block}); err == nil && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
	return block, session, nil
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

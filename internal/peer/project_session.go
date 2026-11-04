package peer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/lorem-dev/duolog/internal/rex"
)

// openProjectPeer runs under peer-open.lock, shared with Close. Peers of all
// leads and linked worktrees share the same project session.
func (e *env) openProjectPeer(ctx context.Context, label string, argv []string) (string, string, error) {
	id, err := e.reg.PeerRexSession()
	if err != nil {
		return "", "", err
	}
	client := e.rex
	client.Label = "duolog | " + label
	client.Anchor = ""
	if id != "" {
		if id == e.rex.Session {
			return "", "", fmt.Errorf("peer session is the lead's Rex session; refusing to change its windows")
		}
		client.Session = id
		block, err := client.NewWindow(ctx, argv)
		if err == nil {
			return block, id, nil
		}
		if !rex.IsNotFound(err) {
			return "", "", err
		}
		if err := e.reg.SetPeerRexSession(""); err != nil {
			return "", "", err
		}
	}
	client.Session = e.rex.Session
	created, err := client.NewSession(ctx, "duolog | "+filepath.Base(filepath.Clean(e.opts.Root)), argv)
	if err != nil {
		return "", "", err
	}
	if err := e.reg.SetPeerRexSession(created.Session); err != nil {
		client.Session = created.Session
		return "", "", errors.Join(err, client.KillSession(ctx))
	}
	e.renameProjectSessions(ctx)
	return created.Block, created.Session, nil
}

// renameProjectSessions is best effort. Rex session IDs are time-ordered, so
// sorting them preserves creation order regardless of the listing's order.
func (e *env) renameProjectSessions(ctx context.Context) {
	sessions, err := e.rex.ListSessions(ctx)
	if err != nil {
		return
	}
	label := "duolog | " + filepath.Base(filepath.Clean(e.opts.Root))
	match := regexp.MustCompile("^" + regexp.QuoteMeta(label) + `(?: \([0-9]+\))?$`)
	var peers []rex.SessionInfo
	for _, session := range sessions {
		if session.ID != e.rex.Session && match.MatchString(session.Label) {
			peers = append(peers, session)
		}
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	for i, session := range peers {
		name := label
		if len(peers) > 1 {
			name += fmt.Sprintf(" (%d)", i+1)
		}
		if session.Label == name {
			continue
		}
		client := e.rex
		client.Session = session.ID
		_ = client.RenameSession(ctx, name)
	}
}

// cleanupProjectSession runs under peer-open.lock. A failed kill leaves its
// identity recorded so that another close can retry cleanup safely.
func (e *env) cleanupProjectSession(ctx context.Context) error {
	id, err := e.reg.PeerRexSession()
	if err != nil || id == "" {
		return err
	}
	if id == e.rex.Session {
		return fmt.Errorf("refusing to close the lead's Rex session")
	}
	entries, err := e.reg.Entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Block != "" && !entry.Closed && entry.RexSession == id {
			return nil
		}
	}
	client := e.rex
	client.Session = id
	if err := client.KillSession(ctx); err != nil {
		return err
	}
	e.renameProjectSessions(ctx)
	return e.reg.SetPeerRexSession("")
}

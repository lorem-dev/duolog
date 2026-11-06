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
	client.Label = label
	client.Anchor = ""
	if id != "" {
		if id == e.rex.Session {
			return "", "", fmt.Errorf("peer session is the lead's Rex session; refusing to change its windows")
		}
		client.Session = id
		block, err := client.NewWindow(ctx, argv)
		if err == nil {
			// A session from before roots were recorded joins its root's numbering.
			_ = e.reg.SetPeerSessionRoot(id, canonicalRoot(e.opts.Root))
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
	created, err := client.NewSession(ctx, e.sessionLabel(), argv)
	if err != nil {
		return "", "", err
	}
	if err := e.reg.SetPeerRexSession(created.Session); err != nil {
		client.Session = created.Session
		return "", "", errors.Join(err, client.KillSession(ctx))
	}
	// Best effort: without the record, the session is only left unnumbered.
	_ = e.reg.SetPeerSessionRoot(created.Session, canonicalRoot(e.opts.Root))
	e.renameProjectSessions(ctx)
	return created.Block, created.Session, nil
}

// sessionLabel is the visible name of the project's peer session: the folder's
// base name, as the person knows the project.
func (e *env) sessionLabel() string {
	return "duolog | " + filepath.Base(filepath.Clean(e.opts.Root))
}

// canonicalRoot is the project's full root with links resolved and cleaned. It,
// not the base name, says which project a session belongs to.
func canonicalRoot(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return filepath.Clean(root)
}

// renameProjectSessions is best effort. It numbers only the sessions recorded
// for this project's canonical root, so two projects whose folders share a base
// name keep their own numbering. Rex session IDs are time-ordered, so sorting
// them preserves creation order regardless of the listing's order.
func (e *env) renameProjectSessions(ctx context.Context) {
	sessions, err := e.rex.ListSessions(ctx)
	if err != nil {
		return
	}
	roots, err := e.reg.PeerSessionRoots()
	if err != nil {
		return
	}
	root := canonicalRoot(e.opts.Root)
	label := e.sessionLabel()
	match := regexp.MustCompile("^" + regexp.QuoteMeta(label) + `(?: \([0-9]+\))?$`)
	var peers []rex.SessionInfo
	for _, session := range sessions {
		if session.ID != e.rex.Session && roots[session.ID] == root && match.MatchString(session.Label) {
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
	_ = e.reg.SetPeerSessionRoot(id, "")
	e.renameProjectSessions(ctx)
	return e.reg.SetPeerRexSession("")
}

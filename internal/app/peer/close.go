package peer

import (
	"context"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/store"
)

// Close closes the pane and the record. The id may be a pane ID, which is what
// doctor prints. A pane that is already gone is not an error, and files stay.
func Close(ctx context.Context, id string) error {
	_, err := CloseNote(ctx, id)
	return err
}

// CloseNote is Close that also says so when the pane was already gone, as
// after a Rex restart. Then it asks Rex to close nothing; the note is empty
// otherwise.
func CloseNote(ctx context.Context, id string) (note string, err error) {
	started, agent, run := time.Now(), "", ""
	defer func() { observeOperation(ctx, "close", agent, run, started, Ready, err) }()
	e, err := setupFor(ctx, false)
	if err != nil {
		return "", err
	}
	unlock, err := store.LockFile(filepath.Join(e.dir, "peer-open.lock"))
	if err != nil {
		return "", err
	}
	defer func() { _ = unlock() }()
	entry, err := e.find(id)
	if err != nil {
		removed, lookupErr := e.reg.PeerRemoved(id)
		if lookupErr != nil {
			return "", lookupErr
		}
		if removed {
			return "", e.cleanupProjectSession(ctx)
		}
		return "", err
	}
	agent, run = entry.Agent, entry.Run
	if err := e.credit(entry); err != nil {
		return "", err
	}
	presence, err := e.clientFor(entry).Probe(ctx, entry.Block)
	if err != nil {
		return "", err
	}
	if presence == rex.Gone {
		note = "its pane was already gone"
	} else if err := e.clientFor(entry).Close(ctx, entry.Block); err != nil {
		return "", err
	}
	// The close is confirmed: the slot frees, a never-sent open is refunded
	// and an unsettled sent task counts at least its estimate when paid.
	if err := e.sessionEnded(ctx, entry); err != nil {
		return note, err
	}
	if err := e.reg.RemovePeer(entry.ID); err != nil {
		return note, err
	}
	// A task still open ends with the pane. An ended pane reported its own.
	if !entry.Closed {
		outcome, code := "interrupted", runner.CodeNone
		if unavailable, uerr := e.reg.Unavailable(); uerr == nil && unavailable[entry.Agent] {
			outcome, code = "infra", runner.CodeQuota
		}
		e.attempt(entry, outcome, code)
	}
	return note, e.cleanupProjectSession(ctx)
}

package peer

import (
	"context"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/store"
)

type sendLock struct{ unlock func() error }

// Readiness, admission, typing and generation updates form one locked send.
func (e *env) beginSend(ctx context.Context, id string, sending *sendLock) (session.Entry, error) {
	entry, err := e.find(id)
	if err != nil {
		return entry, err
	}
	unlock, err := store.LockFileContext(ctx, filepath.Join(e.dir, "peer-send-"+entry.ID+".lock"), 10*time.Millisecond)
	if err != nil {
		return entry, err
	}
	sending.unlock = unlock
	return e.find(entry.ID)
}

func (s *sendLock) finish(ctx context.Context, agent, run string, started time.Time, err error) {
	if s.unlock != nil {
		_ = s.unlock()
	}
	observeOperation(ctx, "send", agent, run, started, Ready, err)
}

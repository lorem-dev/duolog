package peer

import (
	"context"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// Clock is the time peer reads and waits on. Sleep returns ctx's error when
// ctx ends first.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// Pauses are the waits between pane reads and keys. Send is the 0.7 s that the
// recorded run needed between rex send and rex send-key.
type Pauses struct {
	Poll, Settle, Send, Key time.Duration
	// WorkGrace is how long a sent task may show an idle prompt before the
	// session counts as finished without having been seen at work.
	WorkGrace time.Duration
}

// Deps is what peer reaches outside the package: its clock and pauses, the
// model lookup, the person's notification, the registry write of a new pane,
// the account gates and the delegation runner. The wiring package fills every
// field with the real function; tests fill them with fakes. The Pane port of
// the design moves to a later task: peer still calls rex.Client.
type Deps struct {
	Clock  Clock
	Pauses Pauses
	// ResolveModel gives the concrete model of a route's target.
	ResolveModel func(ctx context.Context, cfg *config.Config, cacheDir string, t config.Target) (string, error)
	// Notify shows the person a notification; it must not block or fail a wait.
	Notify func(title, message string)
	// RecordPeer records a new pane's entry; tests replace it to fail.
	RecordPeer func(reg *session.Registry, e session.Entry, limit int) error
	// OnEnvFile runs once rex answered an open, before the pane's env file is
	// checked; tests replace it to stand for a launcher that took the file.
	OnEnvFile func(path string)
	// Delegate runs one delegation: delegate.Run bound to its Deps.
	Delegate delegate.Runner
	// PaidGate opens the account admission gate that delegate uses.
	PaidGate func(*config.Config) (*paidruns.Gate, error)
	// HeldGate shows that the gate of a pane that only it keeps read-only runs,
	// as delegate.HeldGate does.
	HeldGate func(agent, root string, env []string) error
}

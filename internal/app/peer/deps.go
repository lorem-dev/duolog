package peer

import (
	"context"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/launch"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/screen"
)

// Clock is the time peer reads and waits on. Sleep returns ctx's error when
// ctx ends first.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// ModelResolver turns a route's target into the concrete model of its agent.
// cacheDir is the folder of the agent's catalog cache, or "" for none.
type ModelResolver interface {
	Resolve(ctx context.Context, cfg *config.Config, cacheDir string, t config.Target) (string, error)
}

// PaneAgent is what peer needs of an agent to open and read its pane.
type PaneAgent interface {
	// Argv is the interactive command of a pane: the admitted process's launch
	// argv, then the pane's own flags. m is the lead's mode the pane opened
	// under.
	Argv(r route.Route, m mode.Mode, p corelaunch.Process) []string
	// Settings are the extra settings the pane's process gets, or nil.
	Settings() map[string]any
	// Offer reads the agent's own question whether to install a plugin.
	Offer(s screen.Screen) (screen.Offer, bool)
}

// Pane is the pane support of one agent: its port and the texts of its
// terminal. An agent with no pane support has a nil Agent.
type Pane struct {
	Agent PaneAgent
	Facts agent.PaneFacts
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
	// Models gives the concrete model of a route's target, by agent.
	Models map[string]ModelResolver
	// Panes gives the pane support of each agent, by agent. An agent that is
	// not here, or has no Agent, cannot open a pane.
	Panes map[string]Pane
	// Quota names the agents that have a quota reader, by agent. An agent that
	// is not here is never read, so its spend could not be judged: a pane of
	// it gets no paid pass, as in delegate.
	Quota map[string]bool
	// ResetTime parses the time a provider limit on a pane's screen says the
	// quota returns, by the pane's agent; without one it gives a fallback.
	ResetTime func(agent, text string, now time.Time) time.Time
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
	// Build makes a pane's process: workerenv.Build, the one Build.
	Build launch.Build
	// DroppedKeys lists the route names an agent's panes no longer get:
	// workerenv.DroppedKeys.
	DroppedKeys launch.DroppedKeys
}

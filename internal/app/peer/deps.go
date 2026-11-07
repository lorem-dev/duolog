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
	corepane "github.com/lorem-dev/duolog/internal/core/pane"
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

// PaneSupport is the pane support of one agent: its port and the texts of its
// terminal. An agent with no pane support has a nil Agent.
type PaneSupport struct {
	Agent PaneAgent
	Facts agent.PaneFacts
}

// PaneID names one pane: its block and the session that holds it.
type PaneID = corepane.ID

// PaneOpen is what a new pane starts with: its argv, which names the env
// file, the tab's title, the working directory, and the session it joins or
// the label of the session it creates.
type PaneOpen = corepane.Open

// Pane is the terminal backend that holds the peers' panes: Rex today. Peer
// opens, drives and reads panes only through it.
type Pane interface {
	PaneCloser
	// Open starts a pane and returns where it is.
	Open(ctx context.Context, o PaneOpen) (PaneID, error)
	// Send types text into a pane without pressing enter.
	Send(ctx context.Context, id PaneID, text string) error
	// Key presses one key, such as enter or ctrl+c.
	Key(ctx context.Context, id PaneID, key string) error
	// Capture returns the last lines non-blank lines of a pane as plain text.
	Capture(ctx context.Context, id PaneID, lines int) ([]byte, error)
	// CaptureStyled is Capture that keeps the styles that tell an agent's dim
	// suggestion from typed text.
	CaptureStyled(ctx context.Context, id PaneID, lines int) ([]byte, error)
	// Probe tells a running program from an ended one and from a closed pane.
	Probe(ctx context.Context, id PaneID) (corepane.Presence, error)
	// Lead is the session of the lead's own pane, and false when no backend
	// can be reached from here.
	Lead() (session string, ok bool)
	// Sessions lists the backend's sessions.
	Sessions(ctx context.Context) ([]corepane.Session, error)
	// RenameSession gives a session a new label.
	RenameSession(ctx context.Context, session, label string) error
	// CloseSession ends a session with every pane in it. A session that is
	// already gone is not an error.
	CloseSession(ctx context.Context, session string) error
	// Clients lists the connected clients.
	Clients(ctx context.Context) ([]corepane.Client, error)
	// Gone reports an error that says the pane or session is not there.
	Gone(err error) bool
	// Refused reports an error that the backend gave or that shows it was not
	// reached, so that the call did nothing.
	Refused(err error) bool
}

// PaneCloser closes panes and shows them to the person.
type PaneCloser interface {
	// Close closes a pane. A pane that is already gone is not an error.
	Close(ctx context.Context, id PaneID) error
	// Focus shows a pane on one client of the backend. It sends no key.
	Focus(ctx context.Context, id PaneID, client string) error
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
// the account gates, the delegation runner and the panes. The wiring package
// fills every field with the real function; tests fill them with fakes.
type Deps struct {
	Clock  Clock
	Pauses Pauses
	// Models gives the concrete model of a route's target, by agent.
	Models map[string]ModelResolver
	// Panes gives the pane support of each agent, by agent. An agent that is
	// not here, or has no Agent, cannot open a pane.
	Panes map[string]PaneSupport
	// Pane is the backend that holds the panes.
	Pane Pane
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
	// Executable is the duolog that a detached queue run starts.
	Executable func() (string, error)
	// StartDetached starts argv in dir as a process of its own that the caller
	// does not wait for, its output appended to logPath, as
	// kit.StartDetached does.
	StartDetached func(argv []string, dir, logPath string) error
}

// Package pane holds the values that cross the Pane port of peer: where a
// pane is, what opens one, and what the pane backend says about panes,
// sessions and clients. The port is in internal/app/peer; a backend such as
// internal/rex implements it with these types.
package pane

// ID names one pane: its block and the session that holds it.
type ID struct {
	Session string
	Block   string
}

// Open is what a new pane starts with. With Session set it opens as a new tab
// of that session; with Session empty it opens as the first tab of a new
// session that SessionLabel names. Title labels the tab and Dir is the
// program's working directory. Argv starts directly, with no shell.
type Open struct {
	Argv         []string
	Title        string
	Dir          string
	Session      string
	SessionLabel string
}

// Presence is what the backend says about a pane.
type Presence int

const (
	// Running: the pane is there and its program runs.
	Running Presence = iota
	// Exited: the pane is there and its program has ended.
	Exited
	// Gone: the pane is closed.
	Gone
)

// Session is one session of the backend.
type Session struct {
	ID    string
	Label string
}

// Client is one connected client of the backend. A client lists every
// session it is attached to.
type Client struct {
	ID       string   // what Focus takes
	Kind     string   // "app", "cli" or empty when the client sent no info
	Name     string   // the person's label, else the program name; messages only
	Sessions []string // session ids the client is attached to
}

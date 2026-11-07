package rex

import (
	"context"
	"errors"

	"github.com/lorem-dev/duolog/internal/core/pane"
)

// Pane is the Pane port of peer over the rex command line. A zero Client
// finds rex and the lead's session as Detect does, on every call; tests set
// Client to a fake rex. Every call names the session of its pane.
type Pane struct{ Client Client }

// errNoRex is what a call returns when no rex can be found. Peer asks Lead
// first and stops with its own message, so a call does not get here.
var errNoRex = errors.New("rex: not inside a Rex session, or no rex executable")

// client is p.Client, or the detected one when it has no binary, with no
// anchor and session as its session.
func (p Pane) client(session string) (Client, error) {
	c := p.Client
	if c.Bin == "" {
		var ok bool
		if c, ok = Detect(); !ok {
			return Client{}, errNoRex
		}
	}
	c.Anchor = ""
	if session != "" {
		c.Session = session
	}
	return c, nil
}

// Lead is the session that REX_SESSION names, and false when the process
// runs outside Rex or finds no rex executable.
func (p Pane) Lead() (string, bool) {
	c, err := p.client("")
	if err != nil {
		return "", false
	}
	return c.Session, true
}

// Open opens a tab in o.Session with rex window new, or a new session that
// o.SessionLabel names with rex new when o.Session is empty.
func (p Pane) Open(ctx context.Context, o pane.Open) (pane.ID, error) {
	c, err := p.client(o.Session)
	if err != nil {
		return pane.ID{}, err
	}
	c.Label, c.Dir = o.Title, o.Dir
	if o.Session != "" {
		block, err := c.NewWindow(ctx, o.Argv)
		if err != nil {
			return pane.ID{}, err
		}
		return pane.ID{Session: o.Session, Block: block}, nil
	}
	created, err := c.NewSession(ctx, o.SessionLabel, o.Argv)
	if err != nil {
		return pane.ID{}, err
	}
	return pane.ID{Session: created.Session, Block: created.Block}, nil
}

// Send types text without pressing enter.
func (p Pane) Send(ctx context.Context, id pane.ID, text string) error {
	c, err := p.client(id.Session)
	if err != nil {
		return err
	}
	return c.Send(ctx, id.Block, text)
}

// Key presses one key.
func (p Pane) Key(ctx context.Context, id pane.ID, key string) error {
	c, err := p.client(id.Session)
	if err != nil {
		return err
	}
	return c.SendKey(ctx, id.Block, key)
}

// Capture is Client.Capture.
func (p Pane) Capture(ctx context.Context, id pane.ID, lines int) ([]byte, error) {
	c, err := p.client(id.Session)
	if err != nil {
		return nil, err
	}
	return c.Capture(ctx, id.Block, lines)
}

// CaptureStyled is Client.CaptureStyled.
func (p Pane) CaptureStyled(ctx context.Context, id pane.ID, lines int) ([]byte, error) {
	c, err := p.client(id.Session)
	if err != nil {
		return nil, err
	}
	return c.CaptureStyled(ctx, id.Block, lines)
}

// Probe is Client.Probe.
func (p Pane) Probe(ctx context.Context, id pane.ID) (pane.Presence, error) {
	c, err := p.client(id.Session)
	if err != nil {
		return pane.Gone, err
	}
	presence, err := c.Probe(ctx, id.Block)
	switch presence {
	case Running:
		return pane.Running, err
	case Exited:
		return pane.Exited, err
	}
	return pane.Gone, err
}

// Close is Client.Close.
func (p Pane) Close(ctx context.Context, id pane.ID) error {
	c, err := p.client(id.Session)
	if err != nil {
		return err
	}
	return c.Close(ctx, id.Block)
}

// Focus is Client.Focus on client.
func (p Pane) Focus(ctx context.Context, id pane.ID, client string) error {
	c, err := p.client(id.Session)
	if err != nil {
		return err
	}
	return c.Focus(ctx, client, id.Block)
}

// Sessions is Client.ListSessions.
func (p Pane) Sessions(ctx context.Context) ([]pane.Session, error) {
	c, err := p.client("")
	if err != nil {
		return nil, err
	}
	listed, err := c.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]pane.Session, 0, len(listed))
	for _, s := range listed {
		out = append(out, pane.Session{ID: s.ID, Label: s.Label})
	}
	return out, nil
}

// RenameSession is Client.RenameSession on session.
func (p Pane) RenameSession(ctx context.Context, session, label string) error {
	c, err := p.client(session)
	if err != nil {
		return err
	}
	c.Session = session
	return c.RenameSession(ctx, label)
}

// CloseSession is Client.KillSession on session.
func (p Pane) CloseSession(ctx context.Context, session string) error {
	c, err := p.client(session)
	if err != nil {
		return err
	}
	c.Session = session
	return c.KillSession(ctx)
}

// Clients is Client.Clients.
func (p Pane) Clients(ctx context.Context) ([]pane.Client, error) {
	c, err := p.client("")
	if err != nil {
		return nil, err
	}
	listed, err := c.Clients(ctx)
	if err != nil {
		return nil, err
	}
	var out []pane.Client
	for _, info := range listed {
		out = append(out, pane.Client{ID: info.ID, Kind: info.Kind, Name: info.Name, Sessions: info.Sessions})
	}
	return out, nil
}

// Gone is IsNotFound: rex said the block or session is not there.
func (Pane) Gone(err error) bool { return IsNotFound(err) }

// Refused reports a rex call that rex answered with a failure or never
// reached, so that it did nothing.
func (Pane) Refused(err error) bool {
	var rexErr *Error
	return errors.As(err, &rexErr) || errors.Is(err, ErrSocket)
}

package peer

import (
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/rex"
	"testing"
)

func TestPeerClientTargetsRecordedSession(t *testing.T) {
	e := &env{rex: rex.Client{Bin: "rex-bin", Session: "lead-session", Anchor: "lead-pane"}}
	c := e.clientFor(session.Entry{RexSession: "peer-session"})
	if c.Session != "peer-session" || c.Anchor != "" || e.rex.Session != "lead-session" || e.rex.Anchor != "lead-pane" {
		t.Fatalf("client=%+v lead=%+v", c, e.rex)
	}
	if c := e.clientFor(session.Entry{}); c.Session != "lead-session" {
		t.Fatalf("legacy=%+v", c)
	}
}

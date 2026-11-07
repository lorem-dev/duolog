package peer

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
)

func TestPeerClientTargetsRecordedSession(t *testing.T) {
	e := &env{leadSession: "lead-session"}
	id := e.paneOf(session.Entry{RexSession: "peer-session", Block: "block:peer"})
	if id != (PaneID{Session: "peer-session", Block: "block:peer"}) || e.leadSession != "lead-session" {
		t.Fatalf("pane=%+v lead=%q", id, e.leadSession)
	}
	if id := e.paneOf(session.Entry{Block: "block:peer"}); id.Session != "lead-session" {
		t.Fatalf("legacy=%+v", id)
	}
}

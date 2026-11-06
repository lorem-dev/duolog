package peer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// goneInOldSession is what real Rex answers for a block in a session that no
// longer exists. Only the old session gets it, so a call that names another
// session finds the block.
var goneInOldSession = testbin.Scenario{Exit: 3, Stderr: `Error: block target "block:peer" not found: session scope: session "session:peers" not found`}

// restarted makes the fake rex what a restarted Rex server is: the old session
// is not listed, and every pane of it is not found. The lead's process died
// with the server, so its entries were closed; the lead's agent came back in a
// new Rex session under the same lead id.
func (r *rig) restarted() {
	r.t.Helper()
	if err := r.reg.Close("lead-1"); err != nil {
		r.t.Fatal(err)
	}
	r.lead("codex", mode.Ask)
	r.t.Setenv("REX_SESSION", "sess-new")
	script := baseScript()
	script["block inspect -s session:peers"] = goneInOldSession
	script["block close -s session:peers"] = goneInOldSession
	script["session list"] = testbin.Scenario{Stdout: `{"sessions":[{"session_id":"sess-new","label":"lead"}]}`}
	r.rex(script)
}

func TestListShowsGonePeersAfterRexRestart(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	r.restarted()
	peers, err := List(r.ctx)
	if err != nil || len(peers) != 1 {
		t.Fatalf("%+v %v", peers, err)
	}
	p := peers[0]
	if p.ID != e.ID || p.State != Gone || p.Display() != "gone" || p.Working {
		t.Fatalf("%+v", p)
	}
	data, err := json.Marshal(peers)
	if err != nil || !strings.Contains(string(data), `"state":"gone"`) {
		t.Fatalf("%s %v", data, err)
	}
	if len(r.named("block close")) != 0 || len(r.keys()) != 0 {
		t.Fatalf("listing touched a pane: %v", r.calls())
	}
}

// A pane that went away without the lead ending shows as gone as well, when
// the lead runs in another Rex session than the peer was opened in.
func TestListKeepsPeersOfOneLeadAcrossRexSessions(t *testing.T) {
	r := newRig(t, baseScript())
	old := r.ready()
	r.t.Setenv("REX_SESSION", "sess-new")
	script := baseScript()
	script["block inspect -s session:peers"] = goneInOldSession
	r.rex(script)
	peers, err := List(r.ctx)
	if err != nil || len(peers) != 1 || peers[0].ID != old.ID || peers[0].State != Gone {
		t.Fatalf("%+v %v", peers, err)
	}
	if r.entry(old.ID).Closed {
		t.Fatal("listing changed the record")
	}
}

func TestListHidesPeersOfOtherLeads(t *testing.T) {
	r := newRig(t, baseScript())
	r.ready()
	for _, other := range []session.Entry{
		{ID: "peer-other", Lead: "lead-2", Agent: "codex", Role: "implement", Block: "block:other", RexSession: "sess"},
		{ID: "peer-ended", Lead: "lead-2", Agent: "codex", Role: "review", Block: "block:ended", RexSession: "sess", Closed: true},
		{ID: "peer-unowned", Agent: "codex", Role: "review", Block: "block:unowned", RexSession: "sess", Closed: true},
	} {
		if err := r.reg.Record(other); err != nil {
			t.Fatal(err)
		}
	}
	r.restarted()
	peers, err := List(r.ctx)
	if err != nil || len(peers) != 1 || peers[0].ID == "peer-other" || peers[0].ID == "peer-ended" || peers[0].ID == "peer-unowned" {
		t.Fatalf("%+v %v", peers, err)
	}
}

func TestCloseGonePeerFreesRecordAndSlotWithoutRex(t *testing.T) {
	r := newRig(t, baseScript())
	r.paidCodex()
	e := r.ready()
	r.wantHeld(1, 1)
	r.restarted()
	note, err := CloseNote(r.ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "already gone") {
		t.Fatalf("note %q", note)
	}
	if n := len(r.named("block close")); n != 0 {
		t.Fatalf("rex was asked to close a pane that does not exist: %v", r.named("block close"))
	}
	if entries, _ := r.reg.Entries(); len(entries) != 0 {
		t.Fatalf("record stays: %+v", entries)
	}
	r.wantHeld(0, 0)
	if peers, err := List(r.ctx); err != nil || len(peers) != 0 {
		t.Fatalf("%+v %v", peers, err)
	}
}

// A pane that is there is closed through Rex, as before, and says nothing
// about a gone pane.
func TestCloseLivePeerStillClosesThePane(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.ready()
	note, err := CloseNote(r.ctx, e.ID)
	if err != nil || note != "" {
		t.Fatalf("%q %v", note, err)
	}
	if n := len(r.named("block close")); n != 1 {
		t.Fatalf("%d closes", n)
	}
}

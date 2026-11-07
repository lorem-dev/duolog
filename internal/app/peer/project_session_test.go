package peer

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestProjectSessionClosesAfterLastPeer(t *testing.T) {
	r := newRig(t, baseScript())
	first := r.ready()
	script := withScreens(claudeIdle(""))
	script["open-tab"] = peertest.Reply{Block: "block:second"}
	r.panes(script)
	second := r.open("plan")
	if len(r.named("open-session")) != 0 || len(r.named("open-tab")) != 1 {
		t.Fatalf("session was not shared: %v", r.calls())
	}
	for _, id := range []string{first.ID, second.ID} {
		if r.entry(id).RexSession != "session:peers" {
			t.Fatal(r.entry(id))
		}
	}
	if err := Close(r.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.named("close-session")) != 0 {
		t.Fatal("session closed with a live peer")
	}
	if err := Close(r.ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if calls := r.named("close-session"); len(calls) != 1 || !slices.Equal(calls[0], []string{"close-session", "session:peers"}) {
		t.Fatal(calls)
	}
	if id, err := r.reg.PeerRexSession(); err != nil || id != "" {
		t.Fatalf("%s %v", id, err)
	}
}

func TestProjectSessionIsSharedAcrossLeads(t *testing.T) {
	r := newRig(t, baseScript())
	r.ready()
	if err := r.reg.RegisterLead(session.Lead{ID: "lead-2", Target: config.Target{Agent: "claude", Model: "opus", Effort: "high"}, Mode: mode.Ask, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_SESSION", "another-lead-session")
	t.Setenv("REX_BLOCK", "another-lead-pane")
	r.pane.LeadSession = "another-lead-session"
	ctx := WithOptions(context.Background(), Options{Deps: r.deps, Root: r.root, Config: r.cfg, Lead: "lead-2"})
	script := baseScript()
	script["open-tab"] = peertest.Reply{Block: "block:second"}
	r.panes(script)
	second, err := Open(ctx, "implement")
	if err != nil {
		t.Fatal(err)
	}
	if r.entry(second.ID).RexSession != "session:peers" || len(r.named("open-session")) != 0 {
		t.Fatalf("%+v %v", second, r.calls())
	}
	for _, args := range r.calls() {
		if slices.Contains(args, "another-lead-session") || slices.Contains(args, "another-lead-pane") {
			t.Fatal("changed the lead's windows", args)
		}
	}
}

func TestMissingProjectSessionIsRecreated(t *testing.T) {
	r := newRig(t, baseScript())
	first := r.ready()
	script := baseScript()
	script["probe block:peer"] = gone
	script["open-tab"] = notFound
	script["open-session"] = peertest.Reply{Session: "session:replacement", Block: "block:replacement"}
	r.panes(script)
	second := r.open("implement")
	if r.entry(second.ID).RexSession != "session:replacement" || !r.entry(first.ID).Closed || len(r.named("open-session")) != 1 {
		t.Fatalf("%+v %v", r.entry(second.ID), r.calls())
	}
}

func TestFailedSessionCloseCanBeRetried(t *testing.T) {
	r := newRig(t, baseScript())
	peer := r.ready()
	script := baseScript()
	script["close-session"] = peertest.Reply{Sequence: []peertest.Reply{refused, {}}}
	r.panes(script)
	if err := Close(r.ctx, peer.ID); err == nil {
		t.Fatal("failed session close succeeded")
	}
	if id, err := r.reg.PeerRexSession(); err != nil || id != "session:peers" {
		t.Fatalf("cleanup identity lost: %s %v", id, err)
	}
	if err := Close(r.ctx, peer.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.named("close")) != 1 || len(r.named("close-session")) != 2 {
		t.Fatal(r.calls())
	}
	if id, err := r.reg.PeerRexSession(); err != nil || id != "" {
		t.Fatalf("%s %v", id, err)
	}
}

func TestProjectSessionCannotBeTheLeadSession(t *testing.T) {
	r := newRig(t, baseScript())
	if err := r.reg.SetPeerRexSession("sess"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(r.ctx, "implement"); err == nil || !strings.Contains(err.Error(), "lead") {
		t.Fatalf("%v", err)
	}
	e, err := setup(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.cleanupProjectSession(r.ctx); err == nil {
		t.Fatal("lead's session could be closed")
	}
	if len(r.calls()) != 0 {
		t.Fatal("lead's session changed", r.calls())
	}
}

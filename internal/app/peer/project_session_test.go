package peer

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestProjectSessionClosesAfterLastPeer(t *testing.T) {
	r := newRig(t, baseScript())
	first := r.ready()
	script := withScreens(claudeIdle(""))
	script["window new"] = testbin.Scenario{Stdout: `{"block_ids":["block:second"]}`}
	r.rex(script)
	second := r.open("plan")
	if len(r.named("new")) != 0 || len(r.named("window new")) != 1 {
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
	if len(r.named("kill")) != 0 {
		t.Fatal("session closed with a live peer")
	}
	if err := Close(r.ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if calls := r.named("kill"); len(calls) != 1 || !slices.Equal(calls[0], []string{"kill", "session:peers"}) {
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
	ctx := WithOptions(context.Background(), Options{Deps: r.deps, Root: r.root, Config: r.cfg, Lead: "lead-2"})
	script := baseScript()
	script["window new"] = testbin.Scenario{Stdout: `{"block_ids":["block:second"]}`}
	r.rex(script)
	second, err := Open(ctx, "implement")
	if err != nil {
		t.Fatal(err)
	}
	if r.entry(second.ID).RexSession != "session:peers" || len(r.named("new")) != 0 {
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
	script["block inspect -s session:peers"] = gone
	script["window new -s session:peers"] = testbin.Scenario{Exit: 3, Stderr: "session not found"}
	script["new"] = testbin.Scenario{Stdout: `{"session_id":"session:replacement","block_ids":["block:replacement"]}`}
	r.rex(script)
	second := r.open("implement")
	if r.entry(second.ID).RexSession != "session:replacement" || !r.entry(first.ID).Closed || len(r.named("new")) != 1 {
		t.Fatalf("%+v %v", r.entry(second.ID), r.calls())
	}
}

func TestFailedSessionCloseCanBeRetried(t *testing.T) {
	r := newRig(t, baseScript())
	peer := r.ready()
	script := baseScript()
	script["kill"] = testbin.Scenario{Sequence: []testbin.Scenario{{Exit: 4}, {}}}
	r.rex(script)
	if err := Close(r.ctx, peer.ID); err == nil {
		t.Fatal("failed session close succeeded")
	}
	if id, err := r.reg.PeerRexSession(); err != nil || id != "session:peers" {
		t.Fatalf("cleanup identity lost: %s %v", id, err)
	}
	if err := Close(r.ctx, peer.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.named("block close")) != 1 || len(r.named("kill")) != 2 {
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

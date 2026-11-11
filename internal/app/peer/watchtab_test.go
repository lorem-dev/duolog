package peer

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
	corepane "github.com/lorem-dev/duolog/internal/core/pane"
)

var watchArgv = []string{"/opt/duolog", "watch", "d-abcdef"}

func TestWatchTabOpensATabInTheProjectSessionAndMovesTheView(t *testing.T) {
	r := newRig(t, baseScript())
	t.Setenv("REX_BLOCK", "block:lead")
	r.panes(viewScript(clientsOn("sess")))

	got, err := WatchTab(r.ctx, "d-abcdef", watchArgv, "")
	if err != nil {
		t.Fatal(err)
	}
	spawns := r.spawns()
	if len(spawns) != 1 || !strings.Contains(strings.Join(spawns[0], " "), "duolog | watch d-abcdef") || !slices.Equal(spawns[0][len(spawns[0])-3:], watchArgv) {
		t.Fatalf("spawns = %v", spawns)
	}
	if focus := r.named("focus"); len(focus) != 1 || focus[0][len(focus[0])-1] != recordedApp {
		t.Fatalf("focus = %v", focus)
	}
	if len(r.keys()) != 0 || len(r.named("send")) != 0 {
		t.Fatalf("the watch tab was typed into: %v", r.calls())
	}
	if want := "d-abcdef shown on client " + recordedApp + ": duolog | watch d-abcdef; to return, use Previous Session in Rex"; got.Line() != want {
		t.Fatalf("line = %q, want %q", got.Line(), want)
	}
}

// A second watch of a run whose tab still runs shows that tab again; once the
// tab is gone, the next watch opens a new one.
func TestWatchTabShowsTheRunsOpenTabAgain(t *testing.T) {
	r := newRig(t, baseScript())
	t.Setenv("REX_BLOCK", "block:lead")
	script := viewScript(clientsOn("sess"))
	script["probe"] = peertest.Reply{Presence: corepane.Running}
	r.panes(script)

	for range 2 {
		if _, err := WatchTab(r.ctx, "d-abcdef", watchArgv, ""); err != nil {
			t.Fatal(err)
		}
	}
	if spawns := r.spawns(); len(spawns) != 1 {
		t.Fatalf("spawns = %v", spawns)
	}
	if focus := r.named("focus"); len(focus) != 2 || !slices.Equal(focus[0], focus[1]) {
		t.Fatalf("focus = %v", focus)
	}

	script["probe"] = peertest.Reply{Presence: corepane.Gone}
	r.panes(script)
	if _, err := WatchTab(r.ctx, "d-abcdef", watchArgv, ""); err != nil {
		t.Fatal(err)
	}
	if spawns := r.spawns(); len(spawns) != 1 {
		t.Fatalf("a gone tab was not replaced: %v", spawns)
	}
}

func TestWatchTabNeedsRexAndNoWorker(t *testing.T) {
	r := newRig(t, baseScript())
	r.panes(viewScript(clientsOn("sess")))
	t.Setenv("REX_SESSION", "")
	if _, err := WatchTab(r.ctx, "d-abcdef", watchArgv, ""); !errors.Is(err, ErrNoRex) {
		t.Fatalf("err = %v, want ErrNoRex", err)
	}
	t.Setenv("REX_SESSION", "sess")
	t.Setenv("DUOLOG_DEPTH", "1")
	if _, err := WatchTab(r.ctx, "d-abcdef", watchArgv, ""); err == nil || errors.Is(err, ErrNoRex) {
		t.Fatalf("worker err = %v", err)
	}
	if len(r.calls()) != 0 {
		t.Fatalf("pane called: %v", r.calls())
	}
}

func TestWatchTabOpensNothingWhenNoSingleClientShowsTheSession(t *testing.T) {
	r := newRig(t, baseScript())
	t.Setenv("REX_BLOCK", "block:lead")
	r.panes(viewScript(nil))
	if _, err := WatchTab(r.ctx, "d-abcdef", watchArgv, ""); err == nil {
		t.Fatal("watch tab chose a client from none")
	}
	if len(r.spawns()) != 0 {
		t.Fatalf("a tab opened: %v", r.spawns())
	}
	// An explicit client needs no listing.
	script := viewScript(nil)
	script["clients"] = peertest.Reply{Err: errors.New("clients must not be listed")}
	r.panes(script)
	if _, err := WatchTab(r.ctx, "d-abcdef", watchArgv, "client:x"); err != nil {
		t.Fatal(err)
	}
}

func TestCloseWatchTabClosesItsOwnBlock(t *testing.T) {
	r := newRig(t, baseScript())
	if err := r.reg.SetPeerRexSession("sess"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_SESSION", "sess")
	t.Setenv("REX_BLOCK", "block:w")
	if err := CloseWatchTab(r.ctx); err != nil {
		t.Fatal(err)
	}
	closed := r.named("close")
	if len(closed) != 1 || closed[0][len(closed[0])-1] != "block:w" {
		t.Fatalf("close = %v (calls %v)", closed, r.calls())
	}
}

func TestCloseWatchTabRefusesOutsideThePeerSession(t *testing.T) {
	r := newRig(t, baseScript())
	if err := r.reg.SetPeerRexSession("sess"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REX_SESSION", "lead")
	t.Setenv("REX_BLOCK", "block:w")
	if err := CloseWatchTab(r.ctx); err == nil {
		t.Fatal("closed a pane outside the peer session")
	}
	t.Setenv("REX_SESSION", "sess")
	t.Setenv("REX_BLOCK", "")
	if err := CloseWatchTab(r.ctx); err == nil {
		t.Fatal("closed without a block")
	}
	if len(r.calls()) != 0 {
		t.Fatalf("pane called: %v", r.calls())
	}
}
